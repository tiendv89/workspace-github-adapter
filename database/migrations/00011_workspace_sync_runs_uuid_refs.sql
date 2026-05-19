-- +goose Up
-- Align sync run feature/task references with the UUID identifiers used by
-- workspace_features and workspace_tasks. Repository-facing names remain in
-- feature_name/task_name on those source tables.

ALTER TABLE workspace_sync_runs
    ADD COLUMN IF NOT EXISTS feature_uuid UUID,
    ADD COLUMN IF NOT EXISTS task_uuid UUID;

UPDATE workspace_sync_runs wsr
SET feature_uuid = wf.id
FROM workspace_features wf
WHERE wsr.workspace_id = wf.workspace_id
  AND wsr.feature_uuid IS NULL
  AND (
      wsr.feature_id = wf.feature_name
      OR wsr.feature_id = wf.id::text
      OR wsr.feature_id = wf.feature_id::text
      OR (wsr.feature_id IS NULL AND wsr.mode = 'targeted' AND wsr.branch = 'feature/' || wf.feature_name)
  );

UPDATE workspace_sync_runs wsr
SET task_uuid = wt.id
FROM workspace_tasks wt
WHERE wsr.workspace_id = wt.workspace_id
  AND wsr.task_uuid IS NULL
  AND (wsr.feature_uuid IS NULL OR wsr.feature_uuid = wt.feature_id)
  AND (
      wsr.task_id = wt.task_name
      OR wsr.task_id = wt.id::text
      OR wsr.task_id = wt.task_id::text
  );

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM workspace_sync_runs
        WHERE feature_id IS NOT NULL AND feature_uuid IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot migrate workspace_sync_runs.feature_id: found rows without matching workspace_features row';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workspace_sync_runs
        WHERE task_id IS NOT NULL AND task_uuid IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot migrate workspace_sync_runs.task_id: found rows without matching workspace_tasks row';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE workspace_sync_runs
    DROP COLUMN feature_id,
    DROP COLUMN task_id;
ALTER TABLE workspace_sync_runs
    RENAME COLUMN feature_uuid TO feature_id;
ALTER TABLE workspace_sync_runs
    RENAME COLUMN task_uuid TO task_id;

ALTER TABLE workspace_sync_runs
    ADD CONSTRAINT workspace_sync_runs_feature_id_fkey
        FOREIGN KEY (feature_id) REFERENCES workspace_features (id) ON DELETE SET NULL,
    ADD CONSTRAINT workspace_sync_runs_task_id_fkey
        FOREIGN KEY (task_id) REFERENCES workspace_tasks (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_sync_runs_feature
    ON workspace_sync_runs (workspace_id, feature_id);
CREATE INDEX IF NOT EXISTS idx_workspace_sync_runs_task
    ON workspace_sync_runs (workspace_id, task_id);

-- +goose Down
DROP INDEX IF EXISTS idx_workspace_sync_runs_task;
DROP INDEX IF EXISTS idx_workspace_sync_runs_feature;

ALTER TABLE workspace_sync_runs
    DROP CONSTRAINT IF EXISTS workspace_sync_runs_task_id_fkey,
    DROP CONSTRAINT IF EXISTS workspace_sync_runs_feature_id_fkey;

ALTER TABLE workspace_sync_runs
    ADD COLUMN IF NOT EXISTS feature_text TEXT,
    ADD COLUMN IF NOT EXISTS task_text TEXT;

UPDATE workspace_sync_runs wsr
SET feature_text = wf.feature_name
FROM workspace_features wf
WHERE wsr.feature_id = wf.id;

UPDATE workspace_sync_runs wsr
SET task_text = wt.task_name
FROM workspace_tasks wt
WHERE wsr.task_id = wt.id;

ALTER TABLE workspace_sync_runs
    DROP COLUMN feature_id,
    DROP COLUMN task_id;
ALTER TABLE workspace_sync_runs
    RENAME COLUMN feature_text TO feature_id;
ALTER TABLE workspace_sync_runs
    RENAME COLUMN task_text TO task_id;
