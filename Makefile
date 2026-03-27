.PHONY: build install test lint

build:
	go build -o build/cogent ./cmd/cogent

install:
	go build -o $(HOME)/.local/bin/cogent ./cmd/cogent

test:
	go test ./internal/...

lint:
	go vet ./...
	staticcheck ./...
