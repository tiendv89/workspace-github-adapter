# workspace-github-adapter

Bridges GitHub management repositories and a PostgreSQL workspace database. Single binary with two subcommands:

- **api** — Gin HTTP server that validates incoming requests, runs metadata preflight checks against GitHub, and enqueues async jobs.
- **worker** — Redis/asynq worker that executes workspace syncs and task syncs.

## Quick start with Docker Compose

Starts PostgreSQL, Redis, `api`, and `worker`:

```bash
cp .env.example .env
# edit .env — set github.webhook_secret and optionally github.token
docker compose up --build
```

## Configuration

Configuration is file-based. Pass the config file path via `--config` (required):

```bash
go run ./cmd --config configs/config.yaml api
go run ./cmd --config configs/config.yaml worker
```

All config values can be overridden with environment variables using the uppercased dot-path with underscores (e.g. `db.host` → `DB_HOST`).

### Config file reference

```yaml
log:
  level: info                    # debug | info | warn | error

api:
  http:
    address: ":8080"
    mode: release                # debug | release

db:
  host: 127.0.0.1
  port: 5432
  db_name: workspace
  user: workspace
  password: ""
  conn_life_time_seconds: 300
  max_idle_conns: 10
  max_open_conns: 30

redis:
  init_address:
    - localhost:6379
  select_db:
  username:
  password:

github:
  token: ""                      # optional; for private repos or higher rate limits
  webhook_secret: ""             # required for api; HMAC secret for POST /webhook

sync:
  stale_threshold_minutes: 30   # minutes after which a successful sync is considered stale
```

### Key environment variable overrides

| Env var | Config path | Description |
|---|---|---|
| `DB_HOST` | `db.host` | PostgreSQL host |
| `DB_PORT` | `db.port` | PostgreSQL port |
| `DB_NAME` | `db.db_name` | PostgreSQL database name |
| `DB_USER` | `db.user` | PostgreSQL user |
| `DB_PASSWORD` | `db.password` | PostgreSQL password |
| `REDIS_INIT_ADDRESS` | `redis.init_address` | Redis address(es) |
| `GITHUB_TOKEN` | `github.token` | GitHub personal access token |
| `GITHUB_WEBHOOK_SECRET` | `github.webhook_secret` | HMAC secret for webhook signature verification |
| `API_HTTP_ADDRESS` | `api.http.address` | HTTP listen address |
| `LOG_LEVEL` | `log.level` | Log level |
| `SYNC_STALE_THRESHOLD_MINUTES` | `sync.stale_threshold_minutes` | Stale sync threshold |

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

Receives GitHub push events and routes them based on the branch pushed:

- **Base branch push** — enqueues a targeted `workspace:sync` for each feature touched by the push.
- **Feature branch push** — enqueues a targeted `workspace:sync` for that feature.
- **Task branch push** — enqueues a `task:sync` (deduplicated with a 24-hour window).

```
POST /webhook
X-Hub-Signature-256: sha256=<hmac>
```

Configure in GitHub: repo → Settings → Webhooks → select **push** events, set the secret to match `github.webhook_secret`.

## Worker job types

| Queue | Job | Concurrency | Description |
|---|---|---|---|
| `default` | `workspace:sync` | 1 | Full or targeted workspace sync from GitHub |
| `task-sync` | `task:sync` | 3 | Single task branch sync, 24h dedup, max 3 retries |

## Run services manually

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

```bash
go run ./cmd --config configs/config.yaml worker
```

```bash
go run ./cmd --config configs/config.yaml api
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
