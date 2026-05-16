-- name: ListFeatureTasks :many
SELECT id, workspace_id, feature_id, task_id, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2
ORDER BY task_id;

-- name: ListWorkspaceTasks :many
SELECT id, workspace_id, feature_id, task_id, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1
ORDER BY feature_id, task_id;

-- name: GetWorkspaceTask :one
SELECT id, workspace_id, feature_id, task_id, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2 AND task_id = $3;

-- name: UpsertWorkspaceTask :one
INSERT INTO workspace_tasks (
    workspace_id, feature_id, task_id, title, repo, status, depends_on,
    blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now(), now())
ON CONFLICT (workspace_id, feature_id, task_id) DO UPDATE SET
    title          = EXCLUDED.title,
    repo           = EXCLUDED.repo,
    status         = EXCLUDED.status,
    depends_on     = EXCLUDED.depends_on,
    blocked_reason = EXCLUDED.blocked_reason,
    branch         = EXCLUDED.branch,
    execution      = EXCLUDED.execution,
    pr             = EXCLUDED.pr,
    workspace_pr   = EXCLUDED.workspace_pr,
    source_path    = EXCLUDED.source_path,
    source_hash    = EXCLUDED.source_hash,
    updated_at     = now()
RETURNING id, workspace_id, feature_id, task_id, title, repo, status, depends_on,
          blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
          created_at, updated_at;

-- name: DeleteFeatureTasksNotIn :exec
DELETE FROM workspace_tasks
WHERE workspace_id = $1
  AND feature_id   = $2
  AND task_id != ALL($3::text[]);

-- name: DeleteAllFeatureTasks :exec
DELETE FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2;
