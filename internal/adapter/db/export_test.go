package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// Exported wrappers for unexported functions — for use in package db_test only.

var ExportedParseUUID = parseUUID
var ExportedUUIDStr = uuidStr
var ExportedPtrStr = ptrStr
var ExportedUnmarshalStringSlice = unmarshalStringSlice
var ExportedRowToTaskSummary = rowToTaskSummary
var ExportedRowToActivityEvent = rowToActivityEvent
var ExportedRowToFeatureSummary = rowToFeatureSummary

func ExportedSyncRunToSourceState(r *database.WorkspaceSyncRun, threshold *time.Duration) domain.SourceState {
	if r == nil {
		return syncRunToSourceState(nil, threshold)
	}
	// syncRunToSourceState expects a value pointer; dereference and re-pointer to
	// satisfy the internal signature that accepts *database.WorkspaceSyncRun.
	return syncRunToSourceState(r, threshold)
}

func ExportedSyncRunToSourceStateFromValue(r database.WorkspaceSyncRun, threshold *time.Duration) domain.SourceState {
	return syncRunToSourceState(&r, threshold)
}

func ExportedUpsertSnapshot(ctx context.Context, q *database.Queries, uid pgtype.UUID, snap *domain.WorkspaceSnapshot) error {
	return upsertSnapshot(ctx, q, uid, snap)
}

// UUIDFromString parses a UUID string into pgtype.UUID — used by tests.
func UUIDFromString(s string) pgtype.UUID {
	var uid pgtype.UUID
	_ = uid.Scan(s)
	return uid
}

// RawJSON is a helper to create json.RawMessage from a string.
func RawJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}
