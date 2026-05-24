# workspace-github-adapter

Bridges GitHub management repositories and a PostgreSQL workspace database. Two binaries:

- **api** — HTTP server that validates incoming requests, runs metadata preflight checks against GitHub, and enqueues async jobs.
- **worker** — Redis/asynq worker that executes workspace syncs and task syncs.

## Quick start with Docker Compose

Starts PostgreSQL, Redis, `api`, and `worker`:

```bash
cp .env.example .env
# edit .env — set GITHUB_WEBHOOK_SECRET and optionally GITHUB_TOKEN
docker compose up --build
```

Or pass env vars inline:

```bash
GITHUB_WEBHOOK_SECRET='shared-webhook-secret' docker compose up --build
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | PostgreSQL connection string |
| `GITHUB_WEBHOOK_SECRET` | yes (service) | — | HMAC secret for `POST /webhook` signature verification |
| `GITHUB_TOKEN` | no | — | GitHub token for private repos or higher API rate limits |
| `REDIS_URL` | no | `redis://127.0.0.1:6379/0` | Redis connection URL |
| `PORT` | no | `8080` | HTTP listen port for api |
| `STALE_THRESHOLD_MINUTES` | no | `30` | Minutes after which a successful sync is considered stale |

## API

### Import a workspace

Reads `workspace.yaml` from the repo for preflight validation, then enqueues a full sync.

```bash
curl -X POST http://localhost:8080/internal/workspaces/import \
  -H 'Content-Type: application/json' \
  -d '{
    "repo_url": "https://github.com/owner/repo",
    "default_branch": "main",
    "name": "My Workspace"
  }'
```

Response includes: `id`, `name`, `slug`, `repo_url`, `default_branch`.

### Trigger a full sync

```bash
curl -X POST http://localhost:8080/internal/workspaces/{workspace_id}/sync
```

### Health check

```bash
curl http://localhost:8080/healthz
```

### GitHub webhook

Receives GitHub push events and routes them to targeted or full syncs based on branch pattern:

```
POST /webhook
X-Hub-Signature-256: sha256=<hmac>
```

Configure in GitHub: repo → Settings → Webhooks → select **push** events, set the secret to match `GITHUB_WEBHOOK_SECRET`.

## Run services manually

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

```bash
DATABASE_URL='postgres://user:pass@localhost:5432/db?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
GITHUB_TOKEN='optional_token' \
go run ./cmd/worker
```

```bash
DATABASE_URL='postgres://user:pass@localhost:5432/db?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
GITHUB_TOKEN='optional_token' \
GITHUB_WEBHOOK_SECRET='shared-webhook-secret' \
go run ./cmd/api
```

## Development

Regenerate database queries after editing `database/queries/*.sql`:

```bash
sqlc generate
```

Lint:

```bash
golangci-lint run
```
