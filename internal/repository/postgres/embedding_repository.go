package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type EmbeddingRepository struct {
	db       *sql.DB
	identity domain.EmbeddingIdentity
}

func NewEmbeddingRepository(db *sql.DB, identity domain.EmbeddingIdentity) *EmbeddingRepository {
	return &EmbeddingRepository{db, identity}
}

// Periodic reconciliation also covers documents uploaded while this feature was off.
func (r *EmbeddingRepository) EnqueueReady(ctx context.Context) error {
	if !r.identity.Valid() {
		return domain.ErrInvalidInput
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO semantic_indexes(document_version_id,model,revision,format_revision,dimensions)
        SELECT v.id,$1,$2,$3,$4 FROM document_versions v JOIN documents d ON d.active_version_id=v.id
        WHERE v.status='ready' AND EXISTS(SELECT 1 FROM document_chunks c WHERE c.document_version_id=v.id)
        ON CONFLICT(document_version_id,model,revision,format_revision) DO NOTHING`, r.identity.Model, r.identity.Revision, r.identity.Format, r.identity.Dimensions)
	return mapError("enqueue semantic indexes", err)
}
func (r *EmbeddingRepository) Claim(ctx context.Context, lease time.Duration) (domain.SemanticJob, error) {
	var j domain.SemanticJob
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
        SELECT s.id FROM semantic_indexes s JOIN document_versions v ON v.id=s.document_version_id
        JOIN documents d ON d.active_version_id=v.id
        WHERE s.model=$1 AND s.revision=$2 AND s.format_revision=$3 AND v.status='ready'
        AND ((s.status='pending' AND s.next_attempt_at<=NOW()) OR (s.status='processing' AND s.lease_until<NOW()))
        ORDER BY s.next_attempt_at,s.created_at,s.id LIMIT 1 FOR UPDATE OF s SKIP LOCKED)
        UPDATE semantic_indexes s SET status='processing', attempts=attempts+1,lease_token=gen_random_uuid(),
        lease_until=NOW()+$4*INTERVAL '1 second',updated_at=NOW() FROM candidate c WHERE s.id=c.id
        RETURNING s.id,s.document_version_id,s.lease_token,s.attempts`, r.identity.Model, r.identity.Revision, r.identity.Format, lease.Seconds()).Scan(&j.ID, &j.VersionID, &j.LeaseToken, &j.Attempts)
	return j, mapError("claim semantic index", err)
}
func (r *EmbeddingRepository) PendingChunks(ctx context.Context, j domain.SemanticJob) ([]domain.DocumentChunk, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT c.id,c.document_version_id,c.chunk_index,d.title||E'\n'||c.content FROM document_chunks c
		JOIN document_versions v ON v.id=c.document_version_id JOIN documents d ON d.id=v.document_id
        WHERE c.document_version_id=$1 AND NOT EXISTS(SELECT 1 FROM semantic_chunks sc WHERE sc.index_id=$2 AND sc.chunk_id=c.id)
        ORDER BY c.chunk_index LIMIT 8`, j.VersionID, j.ID)
	if err != nil {
		return nil, mapError("pending semantic chunks", err)
	}
	defer rows.Close()
	var out []domain.DocumentChunk
	for rows.Next() {
		var c domain.DocumentChunk
		if err = rows.Scan(&c.ID, &c.DocumentVersionID, &c.ChunkIndex, &c.Content); err != nil {
			return nil, mapError("read semantic chunk", err)
		}
		out = append(out, c)
	}
	return out, mapError("read semantic chunks", rows.Err())
}

// A lease fences every mutation. Chunk manifest and all windows commit together.
func (r *EmbeddingRepository) SaveChunk(ctx context.Context, j domain.SemanticJob, chunk uuid.UUID, windows []domain.EmbeddingWindow, vectors [][]float64, lease time.Duration) error {
	if len(windows) == 0 || len(windows) != len(vectors) || len(windows) > 128 {
		return domain.ErrInvalidInput
	}
	end := 0
	total := windows[0].TotalTokens
	for i, w := range windows {
		if w.WindowIndex != i || w.TotalTokens != total || w.StartToken < 0 || w.EndToken <= w.StartToken || w.EndToken-w.StartToken > 508 || w.EndToken > total ||
			(i == 0 && w.StartToken != 0) || (i > 0 && w.StartToken != end-64) || domain.ValidateEmbedding(vectors[i]) != nil {
			return domain.ErrInvalidInput
		}
		end = w.EndToken
	}
	if end != total {
		return domain.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin semantic save", err)
	}
	defer tx.Rollback()
	if err = r.lock(ctx, tx, j, lease); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO semantic_chunks(index_id,document_version_id,chunk_id,window_count,total_tokens)
        VALUES($1,$2,$3,$4,$5) ON CONFLICT(index_id,chunk_id) DO NOTHING`, j.ID, j.VersionID, chunk, len(windows), total)
	if err != nil {
		return mapError("save semantic manifest", err)
	}
	// Repeated delivery is idempotent. Never modify document_chunks.
	for i, w := range windows {
		v, _ := json.Marshal(vectors[i])
		_, err = tx.ExecContext(ctx, `INSERT INTO embedding_windows(index_id,document_version_id,chunk_id,window_index,start_token,end_token,embedding)
            VALUES($1,$2,$3,$4,$5,$6,$7::public.vector) ON CONFLICT(index_id,chunk_id,window_index) DO NOTHING`, j.ID, j.VersionID, chunk, i, w.StartToken, w.EndToken, string(v))
		if err != nil {
			return mapError("save semantic window", err)
		}
	}
	return mapError("commit semantic chunk", tx.Commit())
}
func (r *EmbeddingRepository) lock(ctx context.Context, tx *sql.Tx, j domain.SemanticJob, lease time.Duration) error {
	result, err := tx.ExecContext(ctx, `UPDATE semantic_indexes SET lease_until=NOW()+$3*INTERVAL '1 second',updated_at=NOW()
        WHERE id=$1 AND lease_token=$2 AND document_version_id=$4 AND status='processing' AND lease_until>NOW()`, j.ID, j.LeaseToken, lease.Seconds(), j.VersionID)
	if err != nil {
		return mapError("fence semantic lease", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
func (r *EmbeddingRepository) Complete(ctx context.Context, j domain.SemanticJob) error {
	result, err := r.db.ExecContext(ctx, `UPDATE semantic_indexes s SET status='ready',completed_at=NOW(),updated_at=NOW(),lease_token=NULL,lease_until=NULL,last_error=NULL
        WHERE s.id=$1 AND s.lease_token=$2 AND s.status='processing' AND s.lease_until>NOW()
        AND EXISTS(SELECT 1 FROM document_chunks c WHERE c.document_version_id=s.document_version_id)
        AND NOT EXISTS(SELECT 1 FROM document_chunks c WHERE c.document_version_id=s.document_version_id AND NOT EXISTS(
            SELECT 1 FROM semantic_chunks sc WHERE sc.index_id=s.id AND sc.chunk_id=c.id AND sc.window_count=(
                SELECT count(*) FROM embedding_windows w WHERE w.index_id=s.id AND w.chunk_id=c.id)))`, j.ID, j.LeaseToken)
	if err != nil {
		return mapError("complete semantic index", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
func (r *EmbeddingRepository) Retry(ctx context.Context, j domain.SemanticJob, code string, delay time.Duration) error {
	result, err := r.db.ExecContext(ctx, `UPDATE semantic_indexes SET status='pending',lease_token=NULL,lease_until=NULL,last_error=$3,
        next_attempt_at=NOW()+$4*INTERVAL '1 second',updated_at=NOW() WHERE id=$1 AND lease_token=$2 AND status='processing' AND lease_until>NOW()`, j.ID, j.LeaseToken, code, delay.Seconds())
	if err != nil {
		return mapError("retry semantic index", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
func (r *EmbeddingRepository) State(ctx context.Context, version uuid.UUID) (domain.SemanticJob, error) {
	var j domain.SemanticJob
	err := r.db.QueryRowContext(ctx, `SELECT s.id,s.document_version_id,s.status,s.attempts,s.last_error,
        (SELECT count(*) FROM semantic_chunks c WHERE c.index_id=s.id),
        (SELECT count(*) FROM document_chunks c WHERE c.document_version_id=s.document_version_id)
        FROM semantic_indexes s WHERE document_version_id=$1 AND model=$2 AND revision=$3 AND format_revision=$4`, version, r.identity.Model, r.identity.Revision, r.identity.Format).
		Scan(&j.ID, &j.VersionID, &j.Status, &j.Attempts, &j.LastError, &j.CompletedChunks, &j.TotalChunks)
	return j, mapError("semantic index state", err)
}

// Explicit maintenance operation; old model/revision indexes and chat sources survive.
func (r *EmbeddingRepository) Reindex(ctx context.Context, version uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return mapError("begin semantic reindex", err)
	}
	defer tx.Rollback()
	var id uuid.UUID
	err = tx.QueryRowContext(ctx, `UPDATE semantic_indexes SET status='pending',completed_at=NULL,lease_token=NULL,lease_until=NULL,
        next_attempt_at=NOW(),last_error=NULL,updated_at=NOW() WHERE document_version_id=$1 AND model=$2 AND revision=$3 AND format_revision=$4 RETURNING id`, version, r.identity.Model, r.identity.Revision, r.identity.Format).Scan(&id)
	if err != nil {
		return mapError("reset semantic index", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM semantic_chunks WHERE index_id=$1`, id); err != nil {
		return mapError("reset semantic chunks", err)
	}
	return mapError("commit semantic reindex", tx.Commit())
}
