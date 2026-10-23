package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSessionCreateReadsNullableFields(t *testing.T) {
	for _, withOptionalFields := range []bool{false, true} {
		db, mock := mockDB(t)
		id, userID := uuid.New(), uuid.New()
		now := time.Now().UTC()
		session := domain.Session{UserID: userID, TokenHash: bytes.Repeat([]byte{1}, 32), ExpiresAt: now.Add(time.Hour)}
		var csrf, revoked any
		if withOptionalFields {
			session.CSRFTokenHash = bytes.Repeat([]byte{2}, 32)
			session.RevokedAt = &now
			csrf, revoked = session.CSRFTokenHash, now
		}
		mock.ExpectQuery(`INSERT INTO sessions (user_id, token_hash, csrf_token_hash, expires_at, revoked_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+sessionColumns).
			WithArgs(userID, session.TokenHash, session.CSRFTokenHash, session.ExpiresAt, session.RevokedAt).
			WillReturnRows(sqlmock.NewRows(strings.Split(sessionColumns, ", ")).
				AddRow(id.String(), userID.String(), session.TokenHash, csrf, now, session.ExpiresAt, revoked))
		got, err := NewSessionRepository(db).Create(context.Background(), session)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != id || got.UserID != userID || !got.CreatedAt.Equal(now) || !bytes.Equal(got.TokenHash, session.TokenHash) || !bytes.Equal(got.CSRFTokenHash, session.CSRFTokenHash) || (got.RevokedAt != nil) != withOptionalFields {
			t.Fatal("session fields were not scanned correctly")
		}
	}
}

func TestSessionLookupNotFound(t *testing.T) {
	db, mock := mockDB(t)
	hash := bytes.Repeat([]byte{1}, 32)
	mock.ExpectQuery(`SELECT ` + sessionColumns + ` FROM sessions WHERE token_hash = $1`).WithArgs(hash).
		WillReturnRows(sqlmock.NewRows(strings.Split(sessionColumns, ", ")))
	if _, err := NewSessionRepository(db).GetByTokenHash(context.Background(), hash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSessionRevokeAffectedRows(t *testing.T) {
	for _, count := range []int64{0, 1} {
		db, mock := mockDB(t)
		id := uuid.New()
		mock.ExpectExec(`UPDATE sessions SET revoked_at = COALESCE(revoked_at, NOW()) WHERE id = $1`).WithArgs(id).
			WillReturnResult(sqlmock.NewResult(0, count))
		err := NewSessionRepository(db).Revoke(context.Background(), id)
		if count == 0 && !errors.Is(err, domain.ErrNotFound) || count == 1 && err != nil {
			t.Fatalf("Revoke affected=%d: %v", count, err)
		}
	}
}

func TestSessionCreateConstraintErrors(t *testing.T) {
	for _, code := range []string{"23505", "23503", "23514"} {
		db, mock := mockDB(t)
		mock.ExpectQuery(`INSERT INTO sessions (user_id, token_hash, csrf_token_hash, expires_at, revoked_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING ` + sessionColumns).
			WillReturnError(&pgconn.PgError{Code: code, Detail: "private-token-hash"})
		_, err := NewSessionRepository(db).Create(context.Background(), domain.Session{})
		want := domain.ErrInvalidInput
		if code == "23505" {
			want = domain.ErrAlreadyExists
		}
		if !errors.Is(err, want) || strings.Contains(err.Error(), "private-token-hash") {
			t.Fatalf("SQLSTATE %s error = %v", code, err)
		}
	}
}

func TestSessionBulkOperations(t *testing.T) {
	db, mock := mockDB(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`).WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := NewSessionRepository(db).RevokeAllForUser(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`DELETE FROM sessions WHERE expires_at <= NOW()`).WillReturnResult(sqlmock.NewResult(0, 3))
	if count, err := NewSessionRepository(db).DeleteExpired(context.Background()); err != nil || count != 3 {
		t.Fatalf("DeleteExpired = %d, %v", count, err)
	}
	want := errors.New("rows affected unavailable")
	mock.ExpectExec(`DELETE FROM sessions WHERE expires_at <= NOW()`).WillReturnResult(sqlmock.NewErrorResult(want))
	if _, err := NewSessionRepository(db).DeleteExpired(context.Background()); !errors.Is(err, want) {
		t.Fatalf("RowsAffected error lost: %v", err)
	}
}
