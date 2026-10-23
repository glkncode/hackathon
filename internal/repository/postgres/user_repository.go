package postgres

import (
	"context"
	"database/sql"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

const userColumns = `id, username, password_hash, role, is_active, created_at, updated_at`

type UserRepository struct {
	db *sql.DB
}

// NewUserRepository borrows a pool; the application owns its lifecycle.
func NewUserRepository(db *sql.DB) *UserRepository { return &UserRepository{db: db} }

func (r *UserRepository) HasUsers(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists)
	return exists, mapError("check users existence", err)
}

// CreateIfEmpty serializes initial inserts using a PostgreSQL table lock, which
// also conflicts with ordinary INSERTs. Read committed rechecks after the lock.
// No existing account is ever updated, promoted or replaced.
func (r *UserRepository) CreateIfEmpty(ctx context.Context, user domain.User) (bool, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, mapError("begin initial user transaction", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return false, mapError("lock initial users", err)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, mapError("check initial users", err)
	}
	if !exists {
		if user.Role == "" {
			user.Role = domain.RoleMember
		}
		if _, err := scanUser("create initial user", tx.QueryRowContext(ctx,
			`INSERT INTO users (username, password_hash, role, is_active) VALUES ($1, $2, $3, $4) RETURNING `+userColumns,
			user.Username, user.PasswordHash, user.Role, user.IsActive)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, mapError("commit initial user", err)
	}
	return !exists, nil
}

// Create ignores input ID/timestamps; PostgreSQL generates them. Empty Role
// defaults to member. IsActive is persisted as supplied, including false.
func (r *UserRepository) Create(ctx context.Context, user domain.User) (domain.User, error) {
	if user.Role == "" {
		user.Role = domain.RoleMember
	}
	row := r.db.QueryRowContext(ctx, `INSERT INTO users (username, password_hash, role, is_active)
		VALUES ($1, $2, $3, $4) RETURNING `+userColumns,
		user.Username, user.PasswordHash, user.Role, user.IsActive)
	return scanUser("create user", row)
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	return scanUser("get user by ID", r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

func (r *UserRepository) GetByUsername(ctx context.Context, username string) (domain.User, error) {
	return scanUser("get user by username", r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE LOWER(username) = LOWER($1)`, username))
}

func (r *UserRepository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1`, id, hash)
	return requireRow("update user password hash", result, err)
}

func (r *UserRepository) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	result, err := r.db.ExecContext(ctx, `UPDATE users SET is_active = $2, updated_at = NOW() WHERE id = $1`, id, active)
	return requireRow("set user active", result, err)
}

func scanUser(operation string, row *sql.Row) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.User{}, mapError(operation, err)
	}
	return user, nil
}

func requireRow(operation string, result sql.Result, err error) error {
	if err != nil {
		return mapError(operation, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return mapError(operation, err)
	}
	if count == 0 {
		return mapError(operation, sql.ErrNoRows)
	}
	return nil
}
