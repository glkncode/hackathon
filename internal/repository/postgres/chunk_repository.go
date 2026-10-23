package postgres

import (
	"context"
	"database/sql"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type ChunkRepository struct{ db *sql.DB }

func NewChunkRepository(db *sql.DB) *ChunkRepository { return &ChunkRepository{db: db} }

// replaceVersionChunks is shared with CompleteJob so replacement, ready status,
// publication and job completion can use the exact same transaction.
func replaceVersionChunks(ctx context.Context, tx *sql.Tx, versionID uuid.UUID, chunks []domain.DocumentChunk) error {
	if err := domain.ValidateChunks(chunks); err != nil {
		return err
	}
	var locked uuid.UUID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM document_versions WHERE id=$1 FOR UPDATE`, versionID).Scan(&locked); err != nil {
		return mapError("lock chunk version", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_version_id=$1`, versionID); err != nil {
		return mapError("remove version chunks", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO document_chunks (document_version_id,chunk_index,content,page_number,section_number,section_title) VALUES ($1,$2,$3,$4,$5,$6)`)
	if err != nil {
		return mapError("prepare version chunks", err)
	}
	defer statement.Close()
	for _, c := range chunks {
		if c.DocumentVersionID != uuid.Nil && c.DocumentVersionID != versionID {
			return domain.ErrInvalidInput
		}
		if _, err := statement.ExecContext(ctx, versionID, c.ChunkIndex, c.Content, c.PageNumber, c.SectionNumber, c.SectionTitle); err != nil {
			return mapError("insert version chunk", err)
		}
	}
	return nil
}

// ReplaceVersionChunks is a trusted maintenance operation. Workers must use
// CompleteJob, which additionally fences writes with the current lease token.
func (r *ChunkRepository) ReplaceVersionChunks(ctx context.Context, versionID uuid.UUID, chunks []domain.DocumentChunk) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin replace chunks", err)
	}
	defer tx.Rollback()
	if err := replaceVersionChunks(ctx, tx, versionID, chunks); err != nil {
		return err
	}
	return mapError("commit replace chunks", tx.Commit())
}
func (r *ChunkRepository) CountChunksByVersion(ctx context.Context, versionID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM document_chunks WHERE document_version_id=$1`, versionID).Scan(&count)
	return count, mapError("count version chunks", err)
}
func (r *ChunkRepository) GetChunksByVersion(ctx context.Context, versionID uuid.UUID) ([]domain.DocumentChunk, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,document_version_id,chunk_index,content,page_number,section_number,section_title,created_at FROM document_chunks WHERE document_version_id=$1 ORDER BY chunk_index`, versionID)
	if err != nil {
		return nil, mapError("get version chunks", err)
	}
	defer rows.Close()
	items := make([]domain.DocumentChunk, 0)
	for rows.Next() {
		var c domain.DocumentChunk
		if err := rows.Scan(&c.ID, &c.DocumentVersionID, &c.ChunkIndex, &c.Content, &c.PageNumber, &c.SectionNumber, &c.SectionTitle, &c.CreatedAt); err != nil {
			return nil, mapError("scan version chunks", err)
		}
		items = append(items, c)
	}
	return items, mapError("read version chunks", rows.Err())
}
