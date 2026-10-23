package postgres

import (
	"context"
	"database/sql"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type DocumentAccessRepository struct{ db *sql.DB }

func NewDocumentAccessRepository(db *sql.DB) *DocumentAccessRepository {
	return &DocumentAccessRepository{db: db}
}

func (r *DocumentAccessRepository) GrantAccess(ctx context.Context, access domain.DocumentAccess) (domain.DocumentAccess, error) {
	// A unique constraint arbitrates simultaneous grants. Recheck member/activity
	// in SQL as well as in the service, without making any actor-role decisions.
	err := r.db.QueryRowContext(ctx, `INSERT INTO document_access (document_id,user_id,granted_by)
        SELECT $1,id,$3 FROM users WHERE id=$2 AND is_active AND role='member'
        RETURNING id,document_id,user_id,granted_by,created_at`, access.DocumentID, access.UserID, access.GrantedBy).
		Scan(&access.ID, &access.DocumentID, &access.UserID, &access.GrantedBy, &access.CreatedAt)
	return access, mapError("grant document access", err)
}
func (r *DocumentAccessRepository) RevokeAccess(ctx context.Context, documentID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM document_access WHERE document_id=$1 AND user_id=$2`, documentID, userID)
	return mapError("revoke document access", err)
}
func (r *DocumentAccessRepository) HasAccess(ctx context.Context, documentID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM document_access a JOIN users u ON u.id=a.user_id WHERE a.document_id=$1 AND a.user_id=$2 AND u.is_active)`, documentID, userID).Scan(&exists)
	return exists, mapError("check document access", err)
}
func (r *DocumentAccessRepository) ListDocumentUsers(ctx context.Context, documentID uuid.UUID, limit, offset int) ([]domain.User, int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, mapError("begin list document users", err)
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM document_access WHERE document_id=$1`, documentID).Scan(&total); err != nil {
		return nil, 0, mapError("count document users", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.role,u.is_active FROM users u JOIN document_access a ON a.user_id=u.id WHERE a.document_id=$1 ORDER BY u.username,u.id LIMIT $2 OFFSET $3`, documentID, limit, offset)
	if err != nil {
		return nil, 0, mapError("list document users", err)
	}
	defer rows.Close()
	items := make([]domain.User, 0)
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.IsActive); err != nil {
			return nil, 0, mapError("scan document user", err)
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, mapError("read document users", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, mapError("close document users", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, mapError("commit list document users", err)
	}
	return items, total, nil
}
