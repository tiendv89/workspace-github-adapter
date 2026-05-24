.PHONY: run-service run-worker lint test

GO = go

run-service:
	$(GO) run ./cmd/adapter-service serve --config configs/config.yaml

run-worker:
	$(GO) run ./cmd/adapter-worker work --config configs/config.yaml

lint:
	golangci-lint run

test:
	$(GO) test ./... -race
