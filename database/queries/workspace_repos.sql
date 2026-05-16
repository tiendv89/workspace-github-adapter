-- name: ListWorkspaceRepos :many
SELECT id, workspace_id, repo_id, base_branch, created_at, updated_at
FROM workspace_repos
WHERE workspace_id = $1
ORDER BY repo_id;

-- name: UpsertWorkspaceRepo :one
INSERT INTO workspace_repos (workspace_id, repo_id, base_branch, created_at, updated_at)
VALUES ($1, $2, $3, now(), now())
ON CONFLICT (workspace_id, repo_id) DO UPDATE SET
    base_branch = EXCLUDED.base_branch,
    updated_at  = now()
RETURNING id, workspace_id, repo_id, base_branch, created_at, updated_at;

-- name: DeleteWorkspaceReposNotIn :exec
DELETE FROM workspace_repos
WHERE workspace_id = $1
  AND repo_id != ALL($2::text[]);
