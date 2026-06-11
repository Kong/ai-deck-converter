.PHONY: build test

build:
	go build -o ai-deck-converter ./cmd/ai-deck-converter

test:
	go test ./... -count 1
