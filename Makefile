.DEFAULT_GOAL := all

APP_NAME := ai-deck-converter

.PHONY: all
all: build test

.PHONY: build
build:
	go build -o $(APP_NAME) ./cmd/ai-deck-converter

.PHONY: test
test:
	go test -failfast -count 1 ./...