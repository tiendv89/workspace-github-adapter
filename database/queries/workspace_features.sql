-- name: ListWorkspaceFeatures :many
SELECT id, workspace_id, feature_id, title, feature_status, current_stage, next_action,
       stages, source_path, source_hash, created_at, updated_at
FROM workspace_features
WHERE workspace_id = $1
ORDER BY updated_at DESC;

-- name: GetWorkspaceFeature :one
SELECT id, workspace_id, feature_id, title, feature_status, current_stage, next_action,
       stages, source_path, source_hash, created_at, updated_at
FROM workspace_features
WHERE workspace_id = $1 AND id::text = $2;

-- name: UpsertWorkspaceFeature :one
INSERT INTO workspace_features (
    workspace_id, feature_id, title, feature_status, current_stage, next_action,
    stages, source_path, source_hash, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
ON CONFLICT (workspace_id, feature_id) DO UPDATE SET
    title          = EXCLUDED.title,
    feature_status = EXCLUDED.feature_status,
    current_stage  = EXCLUDED.current_stage,
    next_action    = EXCLUDED.next_action,
    stages         = EXCLUDED.stages,
    source_path    = EXCLUDED.source_path,
    source_hash    = EXCLUDED.source_hash,
    updated_at     = now()
RETURNING id, workspace_id, feature_id, title, feature_status, current_stage, next_action,
          stages, source_path, source_hash, created_at, updated_at;

-- name: DeleteWorkspaceFeaturesNotIn :exec
DELETE FROM workspace_features
WHERE workspace_id = $1
  AND feature_id != ALL($2::text[]);
