-- +goose Up
-- Store stable UUID identifiers separately from repository-facing names.
-- feature_id/task_id become UUIDs, while feature_name/task_name keep values like feature slugs and T1.

ALTER TABLE workspace_features
    ADD COLUMN IF NOT EXISTS feature_name TEXT;

UPDATE workspace_features
SET feature_name = feature_id
WHERE feature_name IS NULL;

ALTER TABLE workspace_features
    ADD COLUMN IF NOT EXISTS feature_uuid UUID;

UPDATE workspace_features
SET feature_uuid = id
WHERE feature_uuid IS NULL;

ALTER TABLE workspace_features
    DROP CONSTRAINT IF EXISTS workspace_features_workspace_feature_unique;

ALTER TABLE workspace_features
    DROP COLUMN feature_id;
ALTER TABLE workspace_features
    RENAME COLUMN feature_uuid TO feature_id;
ALTER TABLE workspace_features
    ALTER COLUMN feature_id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN feature_id SET NOT NULL,
    ALTER COLUMN feature_name SET NOT NULL;

ALTER TABLE workspace_features
    ADD CONSTRAINT workspace_features_workspace_feature_name_unique UNIQUE (workspace_id, feature_name),
    ADD CONSTRAINT workspace_features_workspace_feature_id_unique UNIQUE (workspace_id, feature_id);

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS task_name TEXT;

UPDATE workspace_tasks
SET task_name = task_id
WHERE task_name IS NULL;

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS task_uuid UUID;

UPDATE workspace_tasks
SET task_uuid = id
WHERE task_uuid IS NULL;

ALTER TABLE workspace_tasks
    DROP CONSTRAINT IF EXISTS workspace_tasks_workspace_feature_task_unique;

ALTER TABLE workspace_tasks
    DROP COLUMN task_id;
ALTER TABLE workspace_tasks
    RENAME COLUMN task_uuid TO task_id;
ALTER TABLE workspace_tasks
    ALTER COLUMN task_id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN task_id SET NOT NULL,
    ALTER COLUMN task_name SET NOT NULL;

ALTER TABLE workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_name),
    ADD CONSTRAINT workspace_tasks_workspace_task_id_unique UNIQUE (workspace_id, task_id);

-- +goose Down
ALTER TABLE workspace_tasks
    DROP CONSTRAINT IF EXISTS workspace_tasks_workspace_task_id_unique,
    DROP CONSTRAINT IF EXISTS workspace_tasks_workspace_feature_task_unique;

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS task_text TEXT;

UPDATE workspace_tasks
SET task_text = task_name;

ALTER TABLE workspace_tasks
    DROP COLUMN task_id;
ALTER TABLE workspace_tasks
    RENAME COLUMN task_text TO task_id;
ALTER TABLE workspace_tasks
    ALTER COLUMN task_id SET NOT NULL,
    DROP COLUMN IF EXISTS task_name;

ALTER TABLE workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_id);

ALTER TABLE workspace_features
    DROP CONSTRAINT IF EXISTS workspace_features_workspace_feature_id_unique,
    DROP CONSTRAINT IF EXISTS workspace_features_workspace_feature_name_unique;

ALTER TABLE workspace_features
    ADD COLUMN IF NOT EXISTS feature_text TEXT;

UPDATE workspace_features
SET feature_text = feature_name;

ALTER TABLE workspace_features
    DROP COLUMN feature_id;
ALTER TABLE workspace_features
    RENAME COLUMN feature_text TO feature_id;
ALTER TABLE workspace_features
    ALTER COLUMN feature_id SET NOT NULL,
    DROP COLUMN IF EXISTS feature_name;

ALTER TABLE workspace_features
    ADD CONSTRAINT workspace_features_workspace_feature_unique UNIQUE (workspace_id, feature_id);
