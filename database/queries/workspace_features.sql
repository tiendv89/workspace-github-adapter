-- name: ListWorkspaceFeatures :many
SELECT id, workspace_id, title, feature_status, current_stage, next_action,
       stages, source_path, source_hash, created_at, updated_at,
       feature_name, feature_id, owner, init_pr_url, init_pr_merged
FROM workspace_features
WHERE workspace_id = $1
ORDER BY updated_at DESC;

-- name: GetWorkspaceFeature :one
SELECT id, workspace_id, title, feature_status, current_stage, next_action,
       stages, source_path, source_hash, created_at, updated_at,
       feature_name, feature_id, owner, init_pr_url, init_pr_merged
FROM workspace_features
WHERE workspace_id = $1 AND feature_id = $2;

-- name: UpsertWorkspaceFeature :one
WITH feature_input AS (
    SELECT COALESCE(
        (SELECT id FROM workspace_features WHERE workspace_id = $1 AND feature_name = $2),
        gen_random_uuid()
    ) AS feature_uuid
)
INSERT INTO workspace_features (
    id, workspace_id, feature_id, feature_name, title, feature_status, current_stage, next_action,
    stages, source_path, source_hash, owner, created_at, updated_at
)
SELECT feature_uuid, $1, feature_uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now()
FROM feature_input
ON CONFLICT (workspace_id, feature_name) DO UPDATE SET
    title          = EXCLUDED.title,
    feature_status = EXCLUDED.feature_status,
    current_stage  = EXCLUDED.current_stage,
    next_action    = EXCLUDED.next_action,
    stages         = EXCLUDED.stages,
    source_path    = EXCLUDED.source_path,
    source_hash    = EXCLUDED.source_hash,
    owner          = COALESCE(workspace_features.owner, EXCLUDED.owner),
    updated_at     = now()
RETURNING id, workspace_id, title, feature_status, current_stage, next_action,
          stages, source_path, source_hash, created_at, updated_at,
          feature_name, feature_id, owner, init_pr_url, init_pr_merged;

-- name: DeleteWorkspaceFeaturesNotIn :exec
DELETE FROM workspace_features
WHERE workspace_id = sqlc.arg(workspace_id)
  AND feature_name != ALL(sqlc.arg(feature_names)::text[])
  AND (owner IS NULL OR owner = '');
