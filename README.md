# 🌐 Minimal WebSocket Chat in Go

## Description

This project is minimal and simple chat application built using Go and WebSockets, with a lightweight frontend powered by HTMX. It allows multiple clients to exchange messages instantly via a web interface. 

## Features

- Real-time messaging over WebSockets
- Lightweight frontend with HTMX integration
- Graceful shutdown using context and OS signals
- Concurrent client handling via multiple goroutines
- Centralized message broadcasting via a Hub
- Safe rendering of messages with Go's templating system
- Used structured logging with `slog`
- Buffered channels and timeout management for WebSocket connections

**Key Go Concepts Demonstrated:**

- Goroutines and channel communication
- `context.Context` for cancellation and timeouts
- Basic conurrency: `sync.Mutex`, `sync.RWMutex`, and `sync.WaitGroup`
- HTTP server setup and WebSocket upgrades
- Error handling and logging practices
- Clean modular structure with internal packages

## Usage

- Run `go mod tidy`
	```go
	go mod tidy
	```
- Build the application
	```bash
	make build
	```
- Run the application
	```bash
	make run
	```
