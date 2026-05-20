# workspace-github-adapter

## Run Redis + worker locally with Docker Compose

Start PostgreSQL, Redis, migrations, HTTP adapter service, and Redis worker:

```bash
GITHUB_WEBHOOK_SECRET='shared-webhook-secret' docker compose up --build
```

Optional GitHub token for private repositories or higher API limits:

```bash
GITHUB_WEBHOOK_SECRET='shared-webhook-secret' \
GITHUB_TOKEN=your_token \
docker compose up --build
```

Import a workspace from a GitHub management repository:

```bash
curl -X POST http://localhost:8080/internal/workspaces/import \
  -H 'Content-Type: application/json' \
  -d '{
    "repo_url": "https://github.com/owner/repo",
    "default_branch": "main"
  }'
```

The import response returns only basic workspace information: `id`, `name`, `slug`, `repo_url`, and `default_branch`.

Trigger a full sync for an existing imported workspace:

```bash
curl -X POST http://localhost:8080/internal/workspaces/00000000-0000-0000-0000-000000000001/sync
```

## Run services manually

Redis:

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

Worker:

```bash
DATABASE_URL='postgres://user:pass@localhost:5432/db?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
GITHUB_TOKEN='optional_token' \
go run ./cmd/adapter-worker
```

HTTP service/enqueuer:

```bash
DATABASE_URL='postgres://user:pass@localhost:5432/db?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
GITHUB_TOKEN='optional_token' \
GITHUB_WEBHOOK_SECRET='shared-webhook-secret' \
go run ./cmd/adapter-service
```

GitHub webhooks should send push events to `POST /webhook` with
`X-Hub-Signature-256` generated from the same `GITHUB_WEBHOOK_SECRET`.
