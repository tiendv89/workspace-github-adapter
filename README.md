# workspace-github-adapter

> **⚠️ DECOMMISSIONED** — This service has been retired as part of the `ts-feature-backward-remove` cleanup.
>
> The workspace-github-adapter bridged GitHub management repositories and a PostgreSQL workspace database to support the legacy TS/git orchestrator. With the Go/Postgres orchestrator now fully supported end-to-end, no ts-owned features remain active and this service's sync pipeline (workspace import, workspace sync, task sync, webhook routing) has no remaining callers.
>
> The `workflow-backend` adapter client (`internal/adapter/`) and the `digital-factory-ui` Import Workspace flow have both been removed, so no route on this service is reachable. The `api` binary now only serves `GET /healthz` (for graceful decommission verification). The `worker` binary exits immediately with a decommission notice.
>
> **Operational checklist (for the human/ops owner):**
> - Confirm no external cron, GitHub webhook configuration, or monitoring dashboard still points at this service's endpoints before tearing down the deployment.
> - Archive this repository once the deployment is confirmed offline.

## Health check

The `api` binary still responds to `GET /healthz → {"status": "ok"}` to allow load-balancer and monitoring probes to confirm the service is reachable during the decommission window.

```bash
curl http://localhost:8080/healthz
```
