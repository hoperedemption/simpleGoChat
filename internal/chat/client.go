package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// client represents a single chat client connect via a WebSocket
type Client struct {
	id   string
	hub  *Hub
	conn *websocket.Conn
	// send is a buffered channel of outbound messages to the client
	send chan []byte
	// logger for client-specific logs
	logger *slog.Logger
}

const (
	// time allowed to write a message to a peer
	writeWait = 10 * time.Second
	// time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second
	// period at which to send pings to the peer. Less than pongWait
	pingPeriod = (pongWait * 9) / 10
	// maximum message size allowed from the peer
	maxMessageSize = 512
	// buffer size for the client's send channel
	sendChannelBufferSize = 256
)

var (
	// used to upgrade a HTTP connection to a WebSocket connection
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			// allow all origin for simplicity
			return true
		},
	}
	// error returned when tyring to send a message to a closed client channel
	ErrClientClosed = errors.New("client send channel is closed")
)

// ServeWs handles websocket requests from the pper
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	id := uuid.New().String()
	logger := slog.Default().With("client_id", id)

	client := &Client{
		id:     id,
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, sendChannelBufferSize),
		logger: logger,
	}

	hub.Register <- client
	logger.Info("client registered")

	// allow collection of memory referenced by the caller by doing all work in
	// go routines
	go client.writePump(context.Background()) // context passed for graceful shutdown
	go client.readPump(context.Background())  // context passed for graceful shutdown
}

// writePump pumps messages from the hub to the websocket connection
// a goroutine running writePump is started for each connection
// the application ensures that there is at most one write operation in fligh per
// connection by executing all write from this goroutine
func (c *Client) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		if err := c.conn.Close(); err != nil {
			c.logger.Error("failed to close client connection during write pump defer", "error", err)
		}
		c.logger.Info("write pump stopped")
	}()

	for {
		// ref for select with context alex mux's video
		select {
		case <-ctx.Done():
			c.logger.Info("write pump context done, closing connection")
			return
		case message, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.logger.Error("failed to set write deadline", "error", err)
				return // continue to defer close
			}
			if !ok {
				// the hub closed the channel
				c.logger.Info("hub closed send channel, sending close message to client")
				if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					c.logger.Error("failed to send close message to client", "error", err)
				}
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				c.logger.Error("failed to get next writer", "error", err)
				return // continue to defer
			}
			if _, err := w.Write(message); err != nil {
				c.logger.Error("failed to write message", "error", err)
				return // continue to defer
			}

			// add queued chat messages to the current websocket message
			n := len(c.send)
			for i := 0; i < n; i++ {
				if _, err := w.Write(<-c.send); err != nil {
					c.logger.Error("failed to write queued message", "error", err)
					return // continue to defer
				}
			}

			if err := w.Close(); err != nil {
				c.logger.Error("failed to close writer", "error", err)
				return // continue to defer close
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.logger.Error("failed to set write deadline for ping", "error", err)
				return // continue to defer close
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logger.Error("failed to send ping message", "error", err)
				return // continue to defer close
			}
		}
	}
}

// readPump pumps messages from the websocket connection to the hub
// the application ensures that there is at most one read operation in flight
// per connection by executing allreads from this goroutine
func (c *Client) readPump(ctx context.Context) {
	defer func() {
		c.hub.Unregister <- c
		if err := c.conn.Close(); err != nil {
			c.logger.Error("failed to close client connection during read pump defer", "error", err)
		}
		c.logger.Info("read pump stopped")
	}()

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		c.logger.Error("failed to set initial read deadline", "error", err)
		return // continue to defer close
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			c.logger.Error("failed to set read deadline in pong handler", "error", err)
			return err
		}
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("read pump context done, closing connection")
			return
		default:
			_, msgBytes, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					c.logger.Error("websocket unexpected closing error", "error", err)
				} else {
					c.logger.Error("websocket read error", "error", err)
				}
				return // exit readPump
			}

			var msg Message
			if err := json.NewDecoder(bytes.NewReader(msgBytes)).Decode(&msg); err != nil {
				c.logger.Error("failed to decode message", "error", err)
				continue
			}

			// for simplicity we can assume HTMX templating handles escaping
			c.hub.Broadcast <- &Message{ClientID: c.id, Text: msg.Text}
			c.logger.Debug("message broadcasted", "message_text", msg.Text)
		}
	}
}

// SendMessage sends a byte slice message to the client's send channel
// it returns errClientClosed if the client's channel is already closed
func (c *Client) SendMessage(msg []byte) error {
	select {
	case c.send <- msg:
		return nil
	default:
		return ErrClientClosed
	}
}

// Close gracefully closes the client's send channel
func (c *Client) Close() {
	c.logger.Info("closing client send channel")
}

// Id getter for the client's unique identifier
func (c *Client) ID() string {
	return c.id
}
