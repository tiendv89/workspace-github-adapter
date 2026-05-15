-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_features (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    feature_id     TEXT        NOT NULL,
    title          TEXT        NOT NULL,
    feature_status TEXT,
    current_stage  TEXT,
    next_action    TEXT,
    stages         JSONB,
    source_path    TEXT        NOT NULL,
    source_hash    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workspace_features_workspace_feature_unique UNIQUE (workspace_id, feature_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_features_status
    ON workspace_features (workspace_id, feature_status);
CREATE INDEX IF NOT EXISTS idx_workspace_features_stage
    ON workspace_features (workspace_id, current_stage);

-- +goose Down
DROP TABLE IF EXISTS workspace_features;
