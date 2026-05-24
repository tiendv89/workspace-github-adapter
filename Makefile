.PHONY: run-service run-worker lint test

GO = go

# Stub targets until cobra subcommands are added in T7
run-service:
	$(GO) run ./cmd/adapter-service

run-worker:
	$(GO) run ./cmd/adapter-worker

lint:
	golangci-lint run

test:
	$(GO) test ./... -race
