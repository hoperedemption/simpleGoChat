.PHONY: run build test clean

# Default target
all: build

# Run the application
run: build
	@echo "Starting chat application on http://localhost:3000"
	@./bin/chat

# Build the application
build:
	@echo "Building application..."
	@mkdir -p bin
	@go build -o bin/chat .

# Clean compiled binaries and caches
clean:
	@echo "Cleaning up..."
	@rm -f bin/chat
	@go clean -modcache
	@echo "Cleaned."