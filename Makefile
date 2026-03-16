.PHONY: build install test lint

build:
	go build -o bin/cagent ./cmd/cagent

install: build
	cp bin/cagent $(HOME)/.local/bin/cagent

test:
	go test ./internal/...

lint:
	go vet ./...
