package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func TestCreateIfEmptyTransaction(t *testing.T) {
	for _, exists := range []bool{false, true} {
		db, mock := mockDB(t)
		repo := NewUserRepository(db)
		mock.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM users)`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
		if got, err := repo.HasUsers(context.Background()); err != nil || got != exists {
			t.Fatal("HasUsers failed")
		}
		mock.ExpectBegin()
		mock.ExpectExec(`LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM users)`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
		if !exists {
			now := time.Now()
			mock.ExpectQuery(`INSERT INTO users (username, password_hash, role, is_active) VALUES ($1, $2, $3, $4) RETURNING `+userColumns).
				WithArgs("admin", "hash", "admin", true).WillReturnRows(sqlmock.NewRows(strings.Split(userColumns, ", ")).AddRow(uuid.New().String(), "admin", "hash", "admin", true, now, now))
		}
		mock.ExpectCommit()
		created, err := repo.CreateIfEmpty(context.Background(), domain.User{Username: "admin", PasswordHash: "hash", Role: domain.RoleAdmin, IsActive: true})
		if err != nil || created == exists {
			t.Fatalf("created=%v, exists=%v, err=%v", created, exists, err)
		}
	}
}

func TestCreateIfEmptyRollsBackOnLockFailure(t *testing.T) {
	db, mock := mockDB(t)
	want := errors.New("lock failed")
	mock.ExpectBegin()
	mock.ExpectExec(`LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`).WillReturnError(want)
	mock.ExpectRollback()
	if created, err := NewUserRepository(db).CreateIfEmpty(context.Background(), domain.User{}); created || !errors.Is(err, want) {
		t.Fatal("lock failure hidden or transaction committed")
	}
}
