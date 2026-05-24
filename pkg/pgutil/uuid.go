package pgutil

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// PgUUID converts a UUID string to a pgtype.UUID, returning a validation error on failure.
func PgUUID(raw string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	if err := uid.Scan(raw); err != nil {
		return pgtype.UUID{}, domain.NewValidationError(domain.ErrValidationMissingInput, "invalid workspace_id: "+raw)
	}
	return uid, nil
}

// UUIDString converts a pgtype.UUID to its string representation.
func UUIDString(uid pgtype.UUID) string {
	return uuid.UUID(uid.Bytes).String()
}
