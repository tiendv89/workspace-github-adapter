-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_tasks (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    feature_id    TEXT        NOT NULL,
    task_id       TEXT        NOT NULL,
    title         TEXT        NOT NULL,
    repo          TEXT,
    status        TEXT,
    depends_on    JSONB       NOT NULL DEFAULT '[]',
    blocked_reason TEXT,
    branch        TEXT,
    execution     JSONB,
    pr            JSONB,
    workspace_pr  JSONB,
    source_path   TEXT        NOT NULL,
    source_hash   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_tasks_feature
    ON workspace_tasks (workspace_id, feature_id);
CREATE INDEX IF NOT EXISTS idx_workspace_tasks_status
    ON workspace_tasks (workspace_id, status);
CREATE INDEX IF NOT EXISTS idx_workspace_tasks_repo
    ON workspace_tasks (workspace_id, repo);

-- +goose Down
DROP TABLE IF EXISTS workspace_tasks;
