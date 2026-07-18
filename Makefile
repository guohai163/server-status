.PHONY: all test build build-agent-linux build-agent-release clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION := $(patsubst v%,%,$(VERSION))
AGENT_LDFLAGS := -s -w -X github.com/guohai/server-status/internal/agent.Version=$(VERSION)

all: test build

test:
	go test ./...

build:
	mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o bin/server-status-server ./cmd/server
	go build -trimpath -ldflags "$(AGENT_LDFLAGS)" -o bin/server-status-agent ./cmd/agent

build-agent-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 go build -trimpath -ldflags "$(AGENT_LDFLAGS)" -o dist/server-status-agent-linux-amd64 ./cmd/agent

build-agent-release:
	./scripts/build-release-agent.sh "$(VERSION)" dist/release

clean:
	rm -rf bin dist coverage.out
