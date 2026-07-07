EXTENSION_DIR := extensions/kongctl-ai-gateway-converter
EXTENSION_BIN := $(EXTENSION_DIR)/bin/kongctl-ext-ai-gateway-converter

.PHONY: build build-extension test

build:
	go build -o ai-deck-converter ./cmd/ai-deck-converter

build-extension:
	mkdir -p $(EXTENSION_DIR)/bin
	CGO_ENABLED=0 go build -o $(EXTENSION_BIN) ./$(EXTENSION_DIR)/cmd/kongctl-ext-ai-gateway-converter

test:
	go test ./...
