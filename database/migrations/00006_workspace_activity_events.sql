-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_activity_events (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    scope_type   TEXT        NOT NULL,
    feature_id   TEXT,
    task_id      TEXT,
    action       TEXT,
    actor        TEXT,
    occurred_at  TEXT,
    note         TEXT,
    sequence     INTEGER     NOT NULL,
    raw_event    JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Partial unique indexes to handle NULL task_id correctly:
-- PostgreSQL UNIQUE constraints treat NULLs as distinct, so partial indexes
-- are required to enforce deduplication when task_id may be NULL.

-- Task-level activity (feature_id and task_id both non-null).
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_task_seq
    ON workspace_activity_events (workspace_id, feature_id, task_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NOT NULL;

-- Feature-level activity (feature_id set, task_id null).
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_feature_seq
    ON workspace_activity_events (workspace_id, feature_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_activity_scope
    ON workspace_activity_events (workspace_id, scope_type, occurred_at);
CREATE INDEX IF NOT EXISTS idx_workspace_activity_feature
    ON workspace_activity_events (workspace_id, feature_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_workspace_activity_task
    ON workspace_activity_events (workspace_id, feature_id, task_id, occurred_at);

-- +goose Down
DROP TABLE IF EXISTS workspace_activity_events;
