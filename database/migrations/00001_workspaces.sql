-- +goose Up
CREATE TABLE IF NOT EXISTS workspaces (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug           TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    management_repo_id TEXT    NOT NULL,
    branch_pattern TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspaces_slug_unique UNIQUE (slug)
);

CREATE INDEX IF NOT EXISTS idx_workspaces_updated_at ON workspaces (updated_at);

-- +goose Down
DROP TABLE IF EXISTS workspaces;
