-- +goose Up
ALTER TABLE workspaces
    ADD COLUMN IF NOT EXISTS slack_channel_id TEXT;

-- +goose Down
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS slack_channel_id;
