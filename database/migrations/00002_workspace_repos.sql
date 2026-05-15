-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_repos (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    repo_id      TEXT        NOT NULL,
    base_branch  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_repos_workspace_repo_unique UNIQUE (workspace_id, repo_id)
);

-- +goose Down
DROP TABLE IF EXISTS workspace_repos;
