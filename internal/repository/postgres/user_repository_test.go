package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return db, mock
}

func TestUserCreateReadsGeneratedValues(t *testing.T) {
	db, mock := mockDB(t)
	id := uuid.New()
	now := time.Now().UTC()
	mock.ExpectQuery(`INSERT INTO users (username, password_hash, role, is_active)
		VALUES ($1, $2, $3, $4) RETURNING `+userColumns).
		WithArgs("Alice", "stored-hash", "member", true).
		WillReturnRows(sqlmock.NewRows(strings.Split(userColumns, ", ")).
			AddRow(id.String(), "Alice", "stored-hash", "member", true, now, now))
	user, err := NewUserRepository(db).Create(context.Background(), domain.User{
		ID: uuid.New(), Username: "Alice", PasswordHash: "stored-hash", IsActive: true,
		CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != id || user.Role != domain.RoleMember || !user.CreatedAt.Equal(now) || !user.UpdatedAt.Equal(now) || !user.IsActive {
		t.Fatal("generated/default values were not read from RETURNING")
	}
}

func TestUserLookupsNotFound(t *testing.T) {
	db, mock := mockDB(t)
	id := uuid.New()
	mock.ExpectQuery(`SELECT ` + userColumns + ` FROM users WHERE id = $1`).WithArgs(id).
		WillReturnRows(sqlmock.NewRows(strings.Split(userColumns, ", ")))
	if _, err := NewUserRepository(db).GetByID(context.Background(), id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID error = %v", err)
	}
	mock.ExpectQuery(`SELECT ` + userColumns + ` FROM users WHERE LOWER(username) = LOWER($1)`).WithArgs("UNKNOWN").
		WillReturnError(sql.ErrNoRows)
	if _, err := NewUserRepository(db).GetByUsername(context.Background(), "UNKNOWN"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByUsername error = %v", err)
	}
}

func TestUserUpdatesNotFoundAndSuccess(t *testing.T) {
	for _, count := range []int64{0, 1} {
		db, mock := mockDB(t)
		r := NewUserRepository(db)
		id := uuid.New()
		mock.ExpectExec(`UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`).
			WithArgs(id, "new-hash").WillReturnResult(sqlmock.NewResult(0, count))
		err := r.UpdatePasswordHash(context.Background(), id, "new-hash")
		if count == 0 && !errors.Is(err, domain.ErrNotFound) || count == 1 && err != nil {
			t.Fatalf("UpdatePasswordHash affected=%d: %v", count, err)
		}
		mock.ExpectExec(`UPDATE users SET is_active = $2, updated_at = NOW() WHERE id = $1`).
			WithArgs(id, false).WillReturnResult(sqlmock.NewResult(0, count))
		err = r.SetActive(context.Background(), id, false)
		if count == 0 && !errors.Is(err, domain.ErrNotFound) || count == 1 && err != nil {
			t.Fatalf("SetActive affected=%d: %v", count, err)
		}
	}
}

func TestDatabaseErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code string
		want error
	}{
		{"23505", domain.ErrAlreadyExists}, {"23503", domain.ErrInvalidInput},
		{"23514", domain.ErrInvalidInput}, {"23502", domain.ErrInvalidInput},
		{"22001", domain.ErrInvalidInput}, {"22P02", domain.ErrInvalidInput},
		{"40001", nil},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cause := &pgconn.PgError{Code: tc.code, Message: "secret-hash", Detail: "secret-hash"}
			err := mapError("test query", cause)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, err)
			}
			var actual *pgconn.PgError
			if !errors.As(err, &actual) || actual != cause {
				t.Fatal("underlying SQLSTATE error was lost")
			}
			if strings.Contains(err.Error(), "secret-hash") || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("unsafe or uninformative error: %v", err)
			}
		})
	}
	// Error text must never be used to classify PostgreSQL failures.
	if errors.Is(mapError("test query", errors.New("23505 unique_violation")), domain.ErrAlreadyExists) {
		t.Fatal("plain error text incorrectly treated as SQLSTATE")
	}
}

func TestUserCreateDuplicate(t *testing.T) {
	db, mock := mockDB(t)
	mock.ExpectQuery(`INSERT INTO users (username, password_hash, role, is_active)
		VALUES ($1, $2, $3, $4) RETURNING `+userColumns).
		WithArgs("Alice", "private-hash", "member", false).
		WillReturnError(&pgconn.PgError{Code: "23505", Detail: "private-hash"})
	_, err := NewUserRepository(db).Create(context.Background(), domain.User{Username: "Alice", PasswordHash: "private-hash"})
	if !errors.Is(err, domain.ErrAlreadyExists) || strings.Contains(err.Error(), "private-hash") {
		t.Fatalf("duplicate user error = %v", err)
	}
}

func TestCanceledRepositoryOperations(t *testing.T) {
	db, _ := mockDB(t)
	users, sessions := NewUserRepository(db), NewSessionRepository(db)
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []func() error{
		func() error { _, err := users.Create(ctx, domain.User{}); return err },
		func() error { _, err := users.GetByID(ctx, id); return err },
		func() error { _, err := users.GetByUsername(ctx, "name"); return err },
		func() error { return users.UpdatePasswordHash(ctx, id, "hash") },
		func() error { return users.SetActive(ctx, id, true) },
		func() error { _, err := sessions.Create(ctx, domain.Session{}); return err },
		func() error { _, err := sessions.GetByTokenHash(ctx, make([]byte, 32)); return err },
		func() error { return sessions.Revoke(ctx, id) },
		func() error { return sessions.RevokeAllForUser(ctx, id) },
		func() error { _, err := sessions.DeleteExpired(ctx); return err },
	}
	for i, operation := range operations {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Errorf("operation %d did not preserve cancellation: %v", i, err)
		}
	}
}
