.PHONY: all build test vet fmt run-mock run-mcp proxy clean

all: fmt vet test build

build:
	go build -o bin/ ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Run the mock target (no QNX hardware needed).
run-mock:
	go run ./cmd/mock-qconn -addr 127.0.0.1:8000

# Run the MCP server against 127.0.0.1:8000 (mock or a forwarded target).
run-mcp:
	go run ./cmd/qconn-mcp --qconn-host 127.0.0.1 --qconn-port 8000 --bind 127.0.0.1:8077 --log-level debug

# Protocol-introspection proxy. Usage: make proxy TARGET=192.168.1.50:8000
proxy:
	go run ./cmd/qconn-proxy -listen 127.0.0.1:8000 -target $(TARGET) -log-format json

clean:
	rm -rf bin
