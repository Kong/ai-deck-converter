.PHONY: build test lint e2e e2e-case e2e-update

# A stale GOROOT in the shell (for example /usr/local/go 1.25) breaks builds when
# `go` on PATH is a newer install (for example Homebrew 1.27).
GO ?= env -u GOROOT go

build:
	$(GO) build -o ai-deck-converter ./cmd/ai-deck-converter

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

e2e:
	$(GO) test -tags=e2e ./e2e -v

e2e-case:
	$(GO) test -tags=e2e ./e2e -v -run 'TestE2E/$(CASE)'

e2e-update:
	$(GO) test -tags=e2e ./e2e -update
