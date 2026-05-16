-- name: GetGitHubSource :one
SELECT id, workspace_id, repo_url, repo_owner, repo_name, default_branch, created_at, updated_at
FROM workspace_github_sources
WHERE workspace_id = $1;

-- name: UpsertGitHubSource :one
INSERT INTO workspace_github_sources (
    workspace_id, repo_url, repo_owner, repo_name, default_branch, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, now(), now())
ON CONFLICT (workspace_id) DO UPDATE SET
    repo_url       = EXCLUDED.repo_url,
    repo_owner     = EXCLUDED.repo_owner,
    repo_name      = EXCLUDED.repo_name,
    default_branch = EXCLUDED.default_branch,
    updated_at     = now()
RETURNING id, workspace_id, repo_url, repo_owner, repo_name, default_branch, created_at, updated_at;
