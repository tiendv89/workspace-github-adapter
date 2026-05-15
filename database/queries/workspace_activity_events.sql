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
WHERE workspace_id = $1 AND feature_id = $2
ORDER BY occurred_at DESC, sequence DESC;

-- name: ListTaskActivityEvents :many
SELECT id, workspace_id, scope_type, feature_id, task_id, action, actor,
       occurred_at, note, sequence, raw_event, created_at
FROM workspace_activity_events
WHERE workspace_id = $1 AND feature_id = $2 AND task_id = $3
ORDER BY sequence;

-- name: UpsertActivityEvent :one
INSERT INTO workspace_activity_events (
    workspace_id, scope_type, feature_id, task_id, action, actor,
    occurred_at, note, sequence, raw_event, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (workspace_id, feature_id, task_id, sequence) DO UPDATE SET
    scope_type  = EXCLUDED.scope_type,
    action      = EXCLUDED.action,
    actor       = EXCLUDED.actor,
    occurred_at = EXCLUDED.occurred_at,
    note        = EXCLUDED.note,
    raw_event   = EXCLUDED.raw_event
RETURNING id, workspace_id, scope_type, feature_id, task_id, action, actor,
          occurred_at, note, sequence, raw_event, created_at;

-- name: DeleteFeatureActivityEventsNotInSequences :exec
DELETE FROM workspace_activity_events
WHERE workspace_id = $1
  AND feature_id   = $2
  AND task_id IS NULL
  AND sequence != ALL($3::integer[]);

-- name: DeleteTaskActivityEventsNotInSequences :exec
DELETE FROM workspace_activity_events
WHERE workspace_id = $1
  AND feature_id   = $2
  AND task_id      = $3
  AND sequence != ALL($4::integer[]);

-- name: DeleteAllFeatureActivityEvents :exec
DELETE FROM workspace_activity_events
WHERE workspace_id = $1 AND feature_id = $2;
