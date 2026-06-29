-- name: ListWorkspaces :many
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id
FROM workspaces
ORDER BY updated_at DESC;

-- name: GetWorkspace :one
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id
FROM workspaces
WHERE id = $1;

-- name: GetWorkspaceBySlug :one
SELECT id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id
FROM workspaces
WHERE slug = $1;

-- name: UpsertWorkspace :one
INSERT INTO workspaces (id, organization_id, slug, name, management_repo_id, branch_pattern, slack_channel_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (slug) DO UPDATE SET
    name               = EXCLUDED.name,
    management_repo_id = EXCLUDED.management_repo_id,
    branch_pattern     = EXCLUDED.branch_pattern,
    slack_channel_id   = EXCLUDED.slack_channel_id,
    -- organization_id intentionally NOT updated on conflict
    updated_at         = now()
RETURNING id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id;

-- name: UpsertWorkspaceByID :one
INSERT INTO workspaces (id, organization_id, slug, name, management_repo_id, branch_pattern, slack_channel_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (id) DO UPDATE SET
    slug               = EXCLUDED.slug,
    name               = EXCLUDED.name,
    management_repo_id = EXCLUDED.management_repo_id,
    branch_pattern     = EXCLUDED.branch_pattern,
    slack_channel_id   = EXCLUDED.slack_channel_id,
    -- organization_id intentionally NOT updated on conflict
    updated_at         = now()
RETURNING id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id;

-- name: UpdateWorkspaceByID :one
UPDATE workspaces
SET slug               = $2,
    name               = $3,
    management_repo_id = $4,
    branch_pattern     = $5,
    slack_channel_id   = $6,
    updated_at         = now()
WHERE id = $1
RETURNING id, slug, name, management_repo_id, branch_pattern, created_at, updated_at, slack_channel_id, organization_id;
