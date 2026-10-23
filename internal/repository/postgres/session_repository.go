package postgres

import (
	"context"
	"database/sql"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

const sessionColumns = `id, user_id, token_hash, csrf_token_hash, created_at, expires_at, revoked_at`

type SessionRepository struct {
	db *sql.DB
}

func NewSessionRepository(db *sql.DB) *SessionRepository { return &SessionRepository{db: db} }

// Create stores hashes as provided and reads the generated ID/CreatedAt from
// PostgreSQL. The caller is responsible for hashing a securely generated token.
func (r *SessionRepository) Create(ctx context.Context, session domain.Session) (domain.Session, error) {
	row := r.db.QueryRowContext(ctx, `INSERT INTO sessions (user_id, token_hash, csrf_token_hash, expires_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+sessionColumns,
		session.UserID, session.TokenHash, session.CSRFTokenHash, session.ExpiresAt, session.RevokedAt)
	return scanSession("create session", row)
}

// GetByTokenHash is a data lookup, not an authentication decision. It deliberately
// also returns expired/revoked sessions and sessions belonging to inactive users.
func (r *SessionRepository) GetByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	return scanSession("get session by token hash", r.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1`, tokenHash))
}

func (r *SessionRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, NOW()) WHERE id = $1`, id)
	return requireRow("revoke session", result, err)
}

// RevokeAllForUser affects only unrevoked, unexpired sessions. A missing user or
// a user without active sessions is a successful no-op.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`, userID)
	return mapError("revoke all user sessions", err)
}

func (r *SessionRepository) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, mapError("delete expired sessions", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, mapError("delete expired sessions", err)
	}
	return count, nil
}

func scanSession(operation string, row *sql.Row) (domain.Session, error) {
	var session domain.Session
	err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFTokenHash,
		&session.CreatedAt, &session.ExpiresAt, &session.RevokedAt)
	if err != nil {
		return domain.Session{}, mapError(operation, err)
	}
	return session, nil
}
