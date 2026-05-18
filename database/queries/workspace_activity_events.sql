-- name: ListActivityEvents :many
SELECT id, workspace_id, scope_type, feature_id, task_id, action, actor,
       occurred_at, note, sequence, raw_event, created_at
FROM workspace_activity_events
WHERE workspace_id = $1
ORDER BY occurred_at DESC, sequence DESC;

-- name: ListFeatureActivityEvents :many
SELECT id, workspace_id, scope_type, feature_id, task_id, action, actor,
       occurred_at, note, sequence, raw_event, created_at
FROM workspace_activity_events
WHERE workspace_id = $1 AND feature_id::text = $2
ORDER BY occurred_at DESC, sequence DESC;

-- name: ListTaskActivityEvents :many
SELECT id, workspace_id, scope_type, feature_id, task_id, action, actor,
       occurred_at, note, sequence, raw_event, created_at
FROM workspace_activity_events
WHERE workspace_id = $1 AND feature_id::text = $2 AND task_id::text = $3
ORDER BY sequence;

-- name: UpsertFeatureActivityEvent :one
-- Targets the partial index: feature_id IS NOT NULL AND task_id IS NULL.
INSERT INTO workspace_activity_events (
    workspace_id, scope_type, feature_id, feature_name, task_id, action, actor,
    occurred_at, note, sequence, raw_event, created_at
)
VALUES ($1, $2, $3, $4, NULL, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (workspace_id, feature_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NULL
DO UPDATE SET
    scope_type   = EXCLUDED.scope_type,
    feature_name = EXCLUDED.feature_name,
    action       = EXCLUDED.action,
    actor       = EXCLUDED.actor,
    occurred_at = EXCLUDED.occurred_at,
    note        = EXCLUDED.note,
    raw_event   = EXCLUDED.raw_event
RETURNING id, workspace_id, scope_type, feature_id, task_id, action, actor,
          occurred_at, note, sequence, raw_event, created_at;

-- name: UpsertTaskActivityEvent :one
-- Targets the partial index: feature_id IS NOT NULL AND task_id IS NOT NULL.
INSERT INTO workspace_activity_events (
    workspace_id, scope_type, feature_id, feature_name, task_id, task_name, action, actor,
    occurred_at, note, sequence, raw_event, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (workspace_id, feature_id, task_id, sequence)
    WHERE feature_id IS NOT NULL AND task_id IS NOT NULL
DO UPDATE SET
    scope_type   = EXCLUDED.scope_type,
    feature_name = EXCLUDED.feature_name,
    task_name    = EXCLUDED.task_name,
    action       = EXCLUDED.action,
    actor       = EXCLUDED.actor,
    occurred_at = EXCLUDED.occurred_at,
    note        = EXCLUDED.note,
    raw_event   = EXCLUDED.raw_event
RETURNING id, workspace_id, scope_type, feature_id, task_id, action, actor,
          occurred_at, note, sequence, raw_event, created_at;

-- name: DeleteAllFeatureActivityEvents :exec
DELETE FROM workspace_activity_events
WHERE workspace_id = $1 AND feature_id::text = $2;
