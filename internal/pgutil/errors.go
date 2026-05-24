package pgutil

import (
	"errors"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
)

// IsDedupeError returns true when the asynq error indicates a duplicate task.
func IsDedupeError(err error) bool {
	return errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) ||
		(err != nil && strings.Contains(err.Error(), "task already exists"))
}

// IsUniqueViolation returns true when err is a PostgreSQL unique constraint violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsUniqueConstraintViolation returns true when err is a unique violation on the named constraint.
func IsUniqueConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
