-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_feature_documents (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    feature_id    TEXT        NOT NULL,
    document_type TEXT        NOT NULL,
    source_path   TEXT        NOT NULL,
    url           TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_feature_documents_unique UNIQUE (workspace_id, feature_id, document_type)
);

CREATE INDEX IF NOT EXISTS idx_workspace_feature_documents_feature
    ON workspace_feature_documents (workspace_id, feature_id);

-- +goose Down
DROP TABLE IF EXISTS workspace_feature_documents;
