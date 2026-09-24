.PHONY: build test lint e2e e2e-case e2e-update

build:
	go build -o ai-deck-converter ./cmd/ai-deck-converter

test:
	go test ./...

lint:
	golangci-lint run ./...

e2e:
	go test -tags=e2e ./e2e -v

e2e-case:
	go test -tags=e2e ./e2e -v -run 'TestE2E/$(CASE)'

e2e-update:
	go test -tags=e2e ./e2e -update
