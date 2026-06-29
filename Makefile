-include .env

.PHONY: run-service run-worker lint test sqlc

GO = go

run-service:
	$(GO) run ./cmd api --config configs/config.yaml

run-worker:
	$(GO) run ./cmd worker --config configs/config.yaml

lint:
	golangci-lint run

test:
	$(GO) test ./... -race

sqlc:
	sqlc generate
