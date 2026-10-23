package postgres

import (
	"context"
	"database/sql"
	"errors"

	"ai-assistant/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

// databaseError keeps the cause available for errors.Is/As without exposing
// PostgreSQL Detail (which may contain password/token hashes) in error strings.
type databaseError struct {
	operation string
	cause     error
	kind      error
	code      string
}

func (e *databaseError) Error() string {
	message := "postgres: " + e.operation + ": "
	switch {
	case e.kind != nil:
		message += e.kind.Error()
	case errors.Is(e.cause, context.Canceled):
		message += "context canceled"
	case errors.Is(e.cause, context.DeadlineExceeded):
		message += "deadline exceeded"
	default:
		message += "database operation failed"
	}
	if e.code != "" {
		message += " (SQLSTATE " + e.code + ")"
	}
	return message
}

func (e *databaseError) Unwrap() []error {
	if e.kind != nil {
		return []error{e.cause, e.kind}
	}
	return []error{e.cause}
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	e := &databaseError{operation: operation, cause: err}
	if errors.Is(err, sql.ErrNoRows) {
		e.kind = domain.ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		e.code = pgerr.Code
		switch pgerr.Code {
		case "23505": // unique_violation
			e.kind = domain.ErrAlreadyExists
		case "23502", "23503", "23514", "22001", "22P02": // input/constraint violations
			e.kind = domain.ErrInvalidInput
		}
	}
	return e
}
