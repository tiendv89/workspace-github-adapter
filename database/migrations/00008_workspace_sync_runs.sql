-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_sync_runs (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    trigger       TEXT        NOT NULL,
    branch        TEXT,
    feature_id    TEXT,
    task_id       TEXT,
    mode          TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'running',
    commit_sha    TEXT,
    changed_paths JSONB,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    error_code    TEXT,
    error_message TEXT,
    metadata      JSONB
);

CREATE INDEX IF NOT EXISTS idx_workspace_sync_runs_workspace_started
    ON workspace_sync_runs (workspace_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_sync_runs_trigger
    ON workspace_sync_runs (workspace_id, trigger);
CREATE INDEX IF NOT EXISTS idx_workspace_sync_runs_status
    ON workspace_sync_runs (workspace_id, status);

-- +goose Down
DROP TABLE IF EXISTS workspace_sync_runs;
