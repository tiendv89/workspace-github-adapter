-- name: ListWorkspaces :many
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at
FROM workspaces
ORDER BY updated_at DESC;

-- name: GetWorkspace :one
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at
FROM workspaces
WHERE id = $1;

-- name: GetWorkspaceBySlug :one
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at
FROM workspaces
WHERE slug = $1;

-- name: UpsertWorkspace :one
INSERT INTO workspaces (id, slug, name, management_repo_id, branch_pattern, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, now(), now())
ON CONFLICT (slug) DO UPDATE SET
    name               = EXCLUDED.name,
    management_repo_id = EXCLUDED.management_repo_id,
    branch_pattern     = EXCLUDED.branch_pattern,
    updated_at         = now()
RETURNING id, slug, name, management_repo_id, branch_pattern, created_at, updated_at;
