.PHONY: run-service run-worker lint test

GO = go

run-service:
	$(GO) run ./cmd api --config configs/config.yaml

run-worker:
	$(GO) run ./cmd worker --config configs/config.yaml

lint:
	golangci-lint run

test:
	$(GO) test ./... -race
