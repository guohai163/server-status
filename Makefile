.PHONY: all test build build-agent-linux clean

all: test build

test:
	go test ./...

build:
	mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o bin/server-status-server ./cmd/server
	go build -trimpath -ldflags "-s -w" -o bin/server-status-agent ./cmd/agent

build-agent-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/server-status-agent-linux-amd64 ./cmd/agent

clean:
	rm -rf bin dist coverage.out
