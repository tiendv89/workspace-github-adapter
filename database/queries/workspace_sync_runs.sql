-- name: InsertSyncRun :one
INSERT INTO workspace_sync_runs (
    workspace_id, trigger, branch, feature_id, task_id, mode, status,
    commit_sha, changed_paths, started_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
RETURNING id, workspace_id, trigger, branch, mode, status,
          commit_sha, changed_paths, started_at, finished_at, error_code, error_message, metadata,
          feature_id, task_id;

-- name: UpdateSyncRunSuccess :one
UPDATE workspace_sync_runs SET
    status      = 'success',
    commit_sha  = $2,
    finished_at = now()
WHERE id = $1
RETURNING id, workspace_id, trigger, branch, mode, status,
          commit_sha, changed_paths, started_at, finished_at, error_code, error_message, metadata,
          feature_id, task_id;

-- name: UpdateSyncRunFailed :one
UPDATE workspace_sync_runs SET
    status        = 'failed',
    error_code    = $2,
    error_message = $3,
    finished_at   = now()
WHERE id = $1
RETURNING id, workspace_id, trigger, branch, mode, status,
          commit_sha, changed_paths, started_at, finished_at, error_code, error_message, metadata,
          feature_id, task_id;

-- name: GetLatestSyncRun :one
SELECT id, workspace_id, trigger, branch, mode, status,
       commit_sha, changed_paths, started_at, finished_at, error_code, error_message, metadata,
       feature_id, task_id
FROM workspace_sync_runs
WHERE workspace_id = $1
ORDER BY finished_at DESC NULLS LAST
LIMIT 1;

-- name: ListLatestSyncRunsPerWorkspace :many
SELECT DISTINCT ON (workspace_id) id, workspace_id, trigger, branch, mode, status,
       commit_sha, changed_paths, started_at, finished_at, error_code, error_message, metadata,
       feature_id, task_id
FROM workspace_sync_runs
ORDER BY workspace_id, finished_at DESC NULLS LAST;
