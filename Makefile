.PHONY: build test lint

build:
	go build -o ai-deck-converter ./cmd/ai-deck-converter

test:
	go test ./...

lint:
	golangci-lint run ./...
