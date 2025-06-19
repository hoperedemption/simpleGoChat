package main

import (
	"context"
	"golangchat/internal/chat"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// serveIndex handles requests for the root path serving the index.html template
// which contains the HTMX client
func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	tmpl, err := template.ParseFiles("internal/templates/index.html")
	if err != nil {
		slog.Error("failed to parse index template", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		slog.Error("failed to execute index template", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

func main() {
	// init structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// call chat.NewHub() to create a new chat hub, if there's an error exit
	hub, err := chat.NewHub()
	if err != nil {
		slog.Error("failed to create new hub", "error", err)
		os.Exit(1)
	}
	// start the hub's main event loop in a separate goroutine
	go hub.Run()

	// when a client requests the root url serveIndex is called
	http.HandleFunc("/", serveIndex)
	// when a client requests /ws chat.ServeWs(hub, w, r) upgrades the HTTP connection to a websocket
	// and associates it with the hub
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		chat.ServeWs(hub, w, r)
	})

	// the Server instance is created with timeouts configure for robustness
	server := &http.Server{
		Addr:         ":3000",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// graceful shutdown see: go by example: Signals

	// channel to listen for OS signals for graceful shutdown
	stop := make(chan os.Signal, 1) // bufsize of 1 allows it to hold one signal without blocking, ok with notify
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("server starting", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	<-stop // block until a signal is received

	slog.Info("shutting down server")

	// gives server 5 seconds to shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}

	// closes the hub's channels to signal goroutines to stop
	hub.Shutdown()
	slog.Info("server gracefully stopped.")
}
