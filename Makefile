.PHONY: run-service run-worker lint test

GO = go

# Stub targets until T7 adds cobra subcommands
run-service:
	$(GO) run ./cmd/adapter-service

run-worker:
	$(GO) run ./cmd/adapter-worker

lint:
	golangci-lint run

test:
	$(GO) test ./... -race
