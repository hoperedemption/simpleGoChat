package chat

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"sync"
	"time"
)

// Hub maintains the set of active clients and broadcasts messages to the clients
// It is responsible for managing client registration, unregistration and broadcasting
// messages to all of the active clients
type Hub struct {
	sync.RWMutex

	// map of registered clients key: Client, value: bool
	clients map[*Client]bool

	// a channel for incoming messages to be broadcasted to all clients
	Broadcast chan *Message
	// a channel for clients requesting to be registered
	Register chan *Client
	// a channel for clients requesting to be unregistered
	Unregister chan *Client

	// stores a history of messages
	messages []*Message

	// the pre-parsed HTML template for chat messages
	messageTemplate *template.Template

	// signals the hub to stop processing
	shutdown chan struct{}
	// tracks active goroutines for graceful shutdown
	wg sync.WaitGroup
	// for hub specific logs
	logger *slog.Logger
}

// indicates a failure to parse the message template
var ErrTemplateParsingFailed = errors.New("message template parsing failed")

// NewHub creates and returns a new, initialized Hub
// it parses the message template once during initialization
func NewHub() (*Hub, error) {
	tmpl, err := template.ParseFiles("internal/templates/message.html")
	if err != nil {
		slog.Error("failed to parse message template", "error", err)
		return nil, fmt.Errorf("%w: %v", ErrTemplateParsingFailed, err)
	}

	return &Hub{
		clients:         make(map[*Client]bool),
		Broadcast:       make(chan *Message),
		Register:        make(chan *Client),
		Unregister:      make(chan *Client),
		messages:        make([]*Message, 0, 100), // pre alloc capacity for messages
		messageTemplate: tmpl,
		shutdown:        make(chan struct{}),
		logger:          slog.Default().With("component", "Hub"),
	}, nil
}

// Run starts the hub's main operating loop, processing registration,
// unregistration, and broadcasting of messages
func (h *Hub) Run() {
	h.wg.Add(1)
	defer h.wg.Done()
	h.logger.Info("hub started")

	for {
		select {
		case <-h.shutdown:
			h.logger.Info("hub shutting down: closing client connections")
			// Close all client send channels upon shutdown
			h.Lock()
			for client := range h.clients {
				client.Close() // this will force writePump to exit
				delete(h.clients, client)
			}
			h.Unlock()
			return

		case client := <-h.Register:
			h.Lock()
			if _, exists := h.clients[client]; exists {
				h.Unlock()
				h.logger.Warn("client already registered", "client_id", client.ID())
				// optionally send an error back to the client or close the connection
				client.Close()
				continue
			}
			h.clients[client] = true
			h.Unlock()
			h.logger.Info("client registered", "client_id", client.ID())

			// send message history to the newly registered client
			// should be done in a non blocking way or in a separate goroutine if history is large
			// for simplicity we send synchronously here assuming history is manageable
			go func(c *Client) {
				h.RLock()
				// a copy of messages -> avoids holding RLock during template Execution
				msgsToSend := make([]*Message, len(h.messages))
				copy(msgsToSend, h.messages)
				h.RUnlock()

				for _, msg := range msgsToSend {
					renderedMsg, err := h.getMessageTemplate(msg)
					if err != nil {
						h.logger.Error("failed to render message in history for client", "client_id", c.ID(), "error", err)
						continue
					}
					if err := c.SendMessage(renderedMsg); err != nil {
						h.logger.Error("failed to send message in history to the client", "client_id", c.ID(), "error", err)
						// if send fails unregister client and return -> assumes client is the problem and avoids having clients
						// with inconsistent message histories
						h.Unregister <- c
						return
					}
				}
			}(client)

		case client := <-h.Unregister:
			h.Lock()
			if _, ok := h.clients[client]; ok {
				client.Close() // ensure client's send channel is closed
				delete(h.clients, client)
				h.logger.Info("client unregistered: ", "client_id", client.ID())
			}
			h.Unlock()

		case msg := <-h.Broadcast:
			// store message history
			h.Lock() // we will be writing to message slices
			// limit history to prevent excessive memory usage
			if len(h.messages) == cap(h.messages) {
				h.messages = h.messages[1:] // remove the oldest message
			}
			h.messages = append(h.messages, msg)
			h.Unlock() // release lock before broadcasting

			// iterate over all clients -> use a read lock
			h.RLock()
			renderedMsg, err := h.getMessageTemplate(msg)
			if err != nil {
				h.logger.Error("failed to render broadcast message template")
				h.RUnlock()
				continue
			}

			// iterate over the clients and send message
			for client := range h.clients {
				// use a non blocking send to avoid blocking the hub if a client is slow
				if err := client.SendMessage(renderedMsg); err != nil {
					h.logger.Warn("failed to send message to client, unregistering", "client_id", client.ID(), "error", err)
					// if an error occurs, i.e the client's send channel is full -> unregister the client
					// in non blocking way to avoid deadlock
					select {
					case h.Unregister <- client:
						h.logger.Debug("client sent for unregistration", "client_id", client.ID())
					case <-time.After(100 * time.Millisecond): // avoid blocking if unregister channel is full
						h.logger.Error("failed to send unregsiter signal for client", "client_id", client.ID())
					}
				}
			}
			h.RUnlock()
		}

	}
}

// getMessageTemplate renders the message using the pre-parsed template
func (h *Hub) getMessageTemplate(msg *Message) ([]byte, error) {
	var renderedMessage bytes.Buffer
	if err := h.messageTemplate.Execute(&renderedMessage, msg); err != nil {
		return nil, fmt.Errorf("failed to execute message template: %w", err)
	}
	return renderedMessage.Bytes(), nil
}

// Shutdown signals the hub to stop and waits for its goroutine to finish
func (h *Hub) Shutdown() {
	h.logger.Info("sending shutdown signal to hub")
	close(h.shutdown) // useful sor signaling
	h.wg.Wait()
	h.logger.Info("hub shutdown complete")

	// close all hub channels after the hub's run loop has exited
	// this prevents panics if other goroutines try to send to closed channels
	// tho ideally senders should check if the hub is shutting down or not
	close(h.Broadcast)
	close(h.Register)
	close(h.Unregister)
}
