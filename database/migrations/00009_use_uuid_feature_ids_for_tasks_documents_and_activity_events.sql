-- +goose Up
-- Convert task/document feature references from feature slugs to workspace_features.id UUIDs.
-- Keep the old slug values in feature_name for API/debug compatibility.

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS feature_name TEXT;

UPDATE workspace_tasks
SET feature_name = feature_id
WHERE feature_name IS NULL;

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS feature_uuid UUID;

UPDATE workspace_tasks wt
SET feature_uuid = wf.id
FROM workspace_features wf
WHERE wt.workspace_id = wf.workspace_id
  AND wt.feature_name = wf.feature_id
  AND wt.feature_uuid IS NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workspace_tasks WHERE feature_uuid IS NULL) THEN
        RAISE EXCEPTION 'cannot migrate workspace_tasks.feature_id: found rows without matching workspace_features row';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE workspace_tasks
    DROP CONSTRAINT IF EXISTS workspace_tasks_workspace_feature_task_unique;
DROP INDEX IF EXISTS idx_workspace_tasks_feature;

ALTER TABLE workspace_tasks
    DROP COLUMN feature_id;
ALTER TABLE workspace_tasks
    RENAME COLUMN feature_uuid TO feature_id;
ALTER TABLE workspace_tasks
    ALTER COLUMN feature_id SET NOT NULL,
    ALTER COLUMN feature_name SET NOT NULL;

ALTER TABLE workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_id),
    ADD CONSTRAINT workspace_tasks_feature_id_fkey FOREIGN KEY (feature_id) REFERENCES workspace_features (id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_workspace_tasks_feature
    ON workspace_tasks (workspace_id, feature_id);

ALTER TABLE workspace_feature_documents
    ADD COLUMN IF NOT EXISTS feature_name TEXT;

UPDATE workspace_feature_documents
SET feature_name = feature_id
WHERE feature_name IS NULL;

ALTER TABLE workspace_feature_documents
    ADD COLUMN IF NOT EXISTS feature_uuid UUID;

UPDATE workspace_feature_documents wfd
SET feature_uuid = wf.id
FROM workspace_features wf
WHERE wfd.workspace_id = wf.workspace_id
  AND wfd.feature_name = wf.feature_id
  AND wfd.feature_uuid IS NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workspace_feature_documents WHERE feature_uuid IS NULL) THEN
        RAISE EXCEPTION 'cannot migrate workspace_feature_documents.feature_id: found rows without matching workspace_features row';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE workspace_feature_documents
    DROP CONSTRAINT IF EXISTS workspace_feature_documents_unique;
DROP INDEX IF EXISTS idx_workspace_feature_documents_feature;

ALTER TABLE workspace_feature_documents
    DROP COLUMN feature_id;
ALTER TABLE workspace_feature_documents
    RENAME COLUMN feature_uuid TO feature_id;
ALTER TABLE workspace_feature_documents
    ALTER COLUMN feature_id SET NOT NULL,
    ALTER COLUMN feature_name SET NOT NULL;

ALTER TABLE workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_unique UNIQUE (workspace_id, feature_id, document_type),
    ADD CONSTRAINT workspace_feature_documents_feature_id_fkey FOREIGN KEY (feature_id) REFERENCES workspace_features (id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_workspace_feature_documents_feature
    ON workspace_feature_documents (workspace_id, feature_id);

-- Activity events are queried by feature/task detail routes, so migrate their IDs too.
ALTER TABLE workspace_activity_events
    ADD COLUMN IF NOT EXISTS feature_name TEXT,
    ADD COLUMN IF NOT EXISTS task_name TEXT;

UPDATE workspace_activity_events
SET feature_name = feature_id
WHERE feature_id IS NOT NULL AND feature_name IS NULL;

UPDATE workspace_activity_events
SET task_name = task_id
WHERE task_id IS NOT NULL AND task_name IS NULL;

ALTER TABLE workspace_activity_events
    ADD COLUMN IF NOT EXISTS feature_uuid UUID,
    ADD COLUMN IF NOT EXISTS task_uuid UUID;

UPDATE workspace_activity_events wae
SET feature_uuid = wf.id
FROM workspace_features wf
WHERE wae.feature_name IS NOT NULL
  AND wae.workspace_id = wf.workspace_id
  AND wae.feature_name = wf.feature_id
  AND wae.feature_uuid IS NULL;

UPDATE workspace_activity_events wae
SET task_uuid = wt.id
FROM workspace_tasks wt
WHERE wae.task_name IS NOT NULL
  AND wae.workspace_id = wt.workspace_id
  AND wae.feature_uuid = wt.feature_id
  AND wae.task_name = wt.task_id
  AND wae.task_uuid IS NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workspace_activity_events WHERE feature_name IS NOT NULL AND feature_uuid IS NULL) THEN
        RAISE EXCEPTION 'cannot migrate workspace_activity_events.feature_id: found rows without matching workspace_features row';
    END IF;
    IF EXISTS (SELECT 1 FROM workspace_activity_events WHERE task_name IS NOT NULL AND task_uuid IS NULL) THEN
        RAISE EXCEPTION 'cannot migrate workspace_activity_events.task_id: found rows without matching workspace_tasks row';
    END IF;
END $$;
-- +goose StatementEnd

DROP INDEX IF EXISTS idx_workspace_activity_task_seq;
DROP INDEX IF EXISTS idx_workspace_activity_feature_seq;
DROP INDEX IF EXISTS idx_workspace_activity_feature;
DROP INDEX IF EXISTS idx_workspace_activity_task;

ALTER TABLE workspace_activity_events
    DROP COLUMN feature_id,
    DROP COLUMN task_id;
ALTER TABLE workspace_activity_events
    RENAME COLUMN feature_uuid TO feature_id;
ALTER TABLE workspace_activity_events
    RENAME COLUMN task_uuid TO task_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_task_seq
    ON workspace_activity_events (workspace_id, feature_id, task_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_feature_seq
    ON workspace_activity_events (workspace_id, feature_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_activity_feature
    ON workspace_activity_events (workspace_id, feature_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_workspace_activity_task
    ON workspace_activity_events (workspace_id, feature_id, task_id, occurred_at);

-- +goose Down
DROP INDEX IF EXISTS idx_workspace_activity_task;
DROP INDEX IF EXISTS idx_workspace_activity_feature;
DROP INDEX IF EXISTS idx_workspace_activity_feature_seq;
DROP INDEX IF EXISTS idx_workspace_activity_task_seq;

ALTER TABLE workspace_activity_events
    ADD COLUMN IF NOT EXISTS feature_text TEXT,
    ADD COLUMN IF NOT EXISTS task_text TEXT;

UPDATE workspace_activity_events
SET feature_text = feature_name
WHERE feature_id IS NOT NULL;

UPDATE workspace_activity_events
SET task_text = task_name
WHERE task_id IS NOT NULL;

ALTER TABLE workspace_activity_events
    DROP COLUMN feature_id,
    DROP COLUMN task_id;
ALTER TABLE workspace_activity_events
    RENAME COLUMN feature_text TO feature_id;
ALTER TABLE workspace_activity_events
    RENAME COLUMN task_text TO task_id;
ALTER TABLE workspace_activity_events
    DROP COLUMN IF EXISTS feature_name,
    DROP COLUMN IF EXISTS task_name;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_task_seq
    ON workspace_activity_events (workspace_id, feature_id, task_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_activity_feature_seq
    ON workspace_activity_events (workspace_id, feature_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_activity_feature
    ON workspace_activity_events (workspace_id, feature_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_workspace_activity_task
    ON workspace_activity_events (workspace_id, feature_id, task_id, occurred_at);

ALTER TABLE workspace_feature_documents
    DROP CONSTRAINT IF EXISTS workspace_feature_documents_feature_id_fkey,
    DROP CONSTRAINT IF EXISTS workspace_feature_documents_unique;
DROP INDEX IF EXISTS idx_workspace_feature_documents_feature;

ALTER TABLE workspace_feature_documents
    ADD COLUMN IF NOT EXISTS feature_text TEXT;

UPDATE workspace_feature_documents
SET feature_text = feature_name;

ALTER TABLE workspace_feature_documents
    DROP COLUMN feature_id;
ALTER TABLE workspace_feature_documents
    RENAME COLUMN feature_text TO feature_id;
ALTER TABLE workspace_feature_documents
    ALTER COLUMN feature_id SET NOT NULL,
    DROP COLUMN IF EXISTS feature_name;

ALTER TABLE workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_unique UNIQUE (workspace_id, feature_id, document_type);

ALTER TABLE workspace_tasks
    DROP CONSTRAINT IF EXISTS workspace_tasks_feature_id_fkey,
    DROP CONSTRAINT IF EXISTS workspace_tasks_workspace_feature_task_unique;
DROP INDEX IF EXISTS idx_workspace_tasks_feature;

ALTER TABLE workspace_tasks
    ADD COLUMN IF NOT EXISTS feature_text TEXT;

UPDATE workspace_tasks
SET feature_text = feature_name;

ALTER TABLE workspace_tasks
    DROP COLUMN feature_id;
ALTER TABLE workspace_tasks
    RENAME COLUMN feature_text TO feature_id;
ALTER TABLE workspace_tasks
    ALTER COLUMN feature_id SET NOT NULL,
    DROP COLUMN IF EXISTS feature_name;

ALTER TABLE workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_id);

CREATE INDEX IF NOT EXISTS idx_workspace_tasks_feature
    ON workspace_tasks (workspace_id, feature_id);
