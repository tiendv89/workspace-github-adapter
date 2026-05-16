-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_github_sources (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    repo_url       TEXT        NOT NULL,
    repo_owner     TEXT        NOT NULL,
    repo_name      TEXT        NOT NULL,
    default_branch TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_github_sources_workspace_unique UNIQUE (workspace_id),
    CONSTRAINT workspace_github_sources_repo_unique UNIQUE (repo_owner, repo_name)
);

-- +goose Down
DROP TABLE IF EXISTS workspace_github_sources;
