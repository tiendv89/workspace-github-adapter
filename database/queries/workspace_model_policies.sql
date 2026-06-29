-- name: GetModelByModelID :one
SELECT id, model_id, display_name, active, created_at, updated_at
FROM models
WHERE model_id = $1;

-- name: DeleteWorkspaceModelPolicies :exec
DELETE FROM workspace_model_policies WHERE workspace_id = $1;

-- name: InsertWorkspaceModelPolicy :exec
INSERT INTO workspace_model_policies (workspace_id, phase, model_id, is_default, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now());
