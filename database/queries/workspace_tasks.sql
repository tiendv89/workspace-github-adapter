-- name: ListFeatureTasks :many
SELECT id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2
ORDER BY CASE WHEN task_name::text ~ '^T[0-9]+$' THEN substring(task_name::text from 2)::int END ASC NULLS LAST, task_name::text ASC;

-- name: ListWorkspaceTasks :many
SELECT id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1
ORDER BY feature_name, CASE WHEN task_name::text ~ '^T[0-9]+$' THEN substring(task_name::text from 2)::int END ASC NULLS LAST, task_name::text ASC;

-- name: GetWorkspaceTask :one
SELECT id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2 AND task_id = $3;

-- name: UpsertWorkspaceTask :one
WITH task_input AS (
    SELECT COALESCE(
        (SELECT id FROM workspace_tasks WHERE workspace_id = $1 AND feature_id = $2 AND task_name = $4),
        gen_random_uuid()
    ) AS task_uuid
)
INSERT INTO workspace_tasks (
    id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
    blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
    created_at, updated_at
)
SELECT task_uuid, $1, $2, $3, task_uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now(), now()
FROM task_input
ON CONFLICT (workspace_id, feature_id, task_name) DO UPDATE SET
    feature_name   = EXCLUDED.feature_name,
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
RETURNING id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
          blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
          created_at, updated_at;

-- name: DeleteFeatureTasksNotIn :exec
DELETE FROM workspace_tasks
WHERE workspace_id = $1
  AND feature_id = $2
  AND task_name != ALL($3::text[]);

-- name: DeleteAllFeatureTasks :exec
DELETE FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2;
