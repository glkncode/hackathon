package postgres

import (
	"ai-assistant/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
)

type documentTxKey struct{}
type sqlExecutor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func queryDB(ctx context.Context, db *sql.DB) sqlExecutor {
	if tx, ok := ctx.Value(documentTxKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}

type ChatDocumentRepository struct{ db *sql.DB }

func NewChatDocumentRepository(db *sql.DB) *ChatDocumentRepository {
	return &ChatDocumentRepository{db}
}

// LLM interpretation occurs before this boundary. The session lock serializes
// all document mutations, while draft CAS also protects edits from the Wizard.
func (r *ChatDocumentRepository) Atomic(ctx context.Context, owner, session uuid.UUID, fn func(context.Context) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var archived sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT s.archived_at FROM chat_sessions s JOIN users u ON u.id=s.user_id AND u.is_active WHERE s.id=$1 AND s.user_id=$2 FOR UPDATE OF s`, session, owner).Scan(&archived)
	if err != nil {
		return mapError("lock document conversation", err)
	}
	if archived.Valid {
		return domain.ErrChatArchived
	}
	if err = fn(context.WithValue(ctx, documentTxKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ChatDocumentRepository) Get(ctx context.Context, owner, session uuid.UUID) (domain.ChatDocumentWorkflow, error) {
	var f domain.ChatDocumentWorkflow
	var ids []byte
	err := queryDB(ctx, r.db).QueryRowContext(ctx, `SELECT w.session_id,w.owner_id,w.draft_id,w.stage,w.version,w.reviewed_revision,w.generated_id,w.updated_at,w.expected_field,w.candidate_ids FROM chat_document_workflows w JOIN chat_sessions s ON s.id=w.session_id AND s.user_id=w.owner_id WHERE w.owner_id=$1 AND w.session_id=$2`, owner, session).Scan(&f.SessionID, &f.OwnerID, &f.DraftID, &f.Stage, &f.Version, &f.ReviewedRevision, &f.GeneratedID, &f.UpdatedAt, &f.ExpectedField, &ids)
	if err == nil {
		err = json.Unmarshal(ids, &f.CandidateIDs)
	}
	return f, mapError("read document workflow", err)
}
func (r *ChatDocumentRepository) Save(ctx context.Context, f domain.ChatDocumentWorkflow) error {
	ids, _ := json.Marshal(f.CandidateIDs)
	if f.CandidateIDs == nil {
		ids = []byte("[]")
	}
	_, err := queryDB(ctx, r.db).ExecContext(ctx, `INSERT INTO chat_document_workflows(session_id,owner_id,draft_id,stage,version,reviewed_revision,generated_id,expected_field,candidate_ids) VALUES($1,$2,$3,$4,1,$5,$6,$7,$8) ON CONFLICT(session_id) DO UPDATE SET draft_id=EXCLUDED.draft_id,stage=EXCLUDED.stage,version=chat_document_workflows.version+1,reviewed_revision=EXCLUDED.reviewed_revision,generated_id=EXCLUDED.generated_id,expected_field=EXCLUDED.expected_field,candidate_ids=EXCLUDED.candidate_ids,updated_at=clock_timestamp() WHERE chat_document_workflows.owner_id=EXCLUDED.owner_id`, f.SessionID, f.OwnerID, f.DraftID, f.Stage, f.ReviewedRevision, f.GeneratedID, f.ExpectedField, ids)
	return mapError("save document workflow", err)
}
