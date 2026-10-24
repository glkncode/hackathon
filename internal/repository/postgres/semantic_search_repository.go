package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type SemanticSearchRepository struct{ db *sql.DB }

func NewSemanticSearchRepository(db *sql.DB) *SemanticSearchRepository {
	return &SemanticSearchRepository{db}
}

// Recheck after a potentially slow evidence call before these sources reach
// Chat generation or the public Search response. Historical chat provenance
// remains governed by the existing ChatRepository policy.
func (r *SemanticSearchRepository) FilterVisible(ctx context.Context, user uuid.UUID, role domain.Role, sources []domain.SearchResult) ([]domain.SearchResult, error) {
	if len(sources) == 0 {
		return []domain.SearchResult{}, nil
	}
	ids := make([]string, 0, len(sources))
	for _, s := range sources {
		ids = append(ids, s.ChunkID.String())
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `SELECT c.id FROM document_chunks c JOIN document_versions v ON v.id=c.document_version_id
		JOIN documents d ON d.id=v.document_id AND d.active_version_id=v.id JOIN users u ON u.id=$1 AND u.is_active AND u.role=$2
		WHERE c.id=ANY($3::uuid[]) AND v.status='ready' AND (u.role='admin' OR EXISTS(SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))`, user, role, ids)
	if err != nil {
		return nil, mapError("revalidate semantic visibility", err)
	}
	defer rows.Close()
	allowed := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, mapError("read semantic visibility", err)
		}
		allowed[id] = true
	}
	if err = rows.Err(); err != nil {
		return nil, mapError("semantic visibility", err)
	}
	out := make([]domain.SearchResult, 0, len(sources))
	for _, s := range sources {
		if allowed[s.ChunkID] {
			out = append(out, s)
		}
	}
	return out, nil
}

const semanticSearchSQL = `WITH allowed AS MATERIALIZED (
    SELECT c.id AS chunk_id,d.id AS document_id,v.id AS version_id,d.title,v.version_number,
        c.chunk_index,c.content,c.page_number,metadata.section_number,metadata.section_title
    FROM document_chunks c
    JOIN document_versions v ON v.id=c.document_version_id
    JOIN documents d ON d.id=v.document_id AND d.active_version_id=v.id
    JOIN users u ON u.id=$1 AND u.is_active AND u.role=$2
    ` + chunkMetadataSQL + `
    WHERE v.status='ready' AND (u.role='admin' OR EXISTS(
        SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))
), candidates AS (
    SELECT c.chunk_id,min(w.embedding OPERATOR(public.<=>) $3::public.vector) AS distance
    FROM allowed c JOIN embedding_windows w ON w.chunk_id=c.chunk_id AND w.document_version_id=c.version_id
    JOIN semantic_indexes s ON s.id=w.index_id AND s.document_version_id=c.version_id
    WHERE s.status='ready' AND s.model=$4 AND s.revision=$5 AND s.format_revision=$6 AND s.dimensions=384
    GROUP BY c.chunk_id ORDER BY distance,c.chunk_id LIMIT $7
)
-- Existing chat citation storage requires a finite nonnegative score. Keep
-- exact distance ordering, and only clamp the public projection (not a gate).
SELECT c.*,greatest(0.0,least(1.0,1-r.distance)) AS score FROM candidates r JOIN allowed c ON c.chunk_id=r.chunk_id
ORDER BY r.distance,c.chunk_id`

func (r *SemanticSearchRepository) SearchSemantic(ctx context.Context, user uuid.UUID, role domain.Role, vector []float64, identity domain.EmbeddingIdentity, limit int) ([]domain.SearchResult, error) {
	if user == uuid.Nil || !role.Valid() || !identity.Valid() || limit < 1 || limit > domain.MaxSearchLimit || domain.ValidateEmbedding(vector) != nil {
		return nil, domain.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	v, _ := json.Marshal(vector)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, mapError("semantic snapshot", err)
	}
	defer tx.Rollback()
	// Until all visible versions have a compatible complete index, use FTS.
	// Both checks run in the same ACL/publication snapshot as vector retrieval.
	var missing bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM documents d JOIN document_versions v ON v.id=d.active_version_id
		JOIN users u ON u.id=$1 AND u.is_active AND u.role=$2
		WHERE v.status='ready' AND (u.role='admin' OR EXISTS(SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))
		AND NOT EXISTS(SELECT 1 FROM semantic_indexes s WHERE s.document_version_id=v.id AND s.model=$3 AND s.revision=$4 AND s.format_revision=$5 AND s.status='ready' AND s.dimensions=384))`, user, role, identity.Model, identity.Revision, identity.Format).Scan(&missing)
	if err != nil {
		return nil, mapError("semantic coverage", err)
	}
	if missing {
		return nil, domain.ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, semanticSearchSQL, user, role, string(v), identity.Model, identity.Revision, identity.Format, limit)
	if err != nil {
		return nil, mapError("semantic search", err)
	}
	defer rows.Close()
	out := make([]domain.SearchResult, 0)
	for rows.Next() {
		var s domain.SearchResult
		if err = rows.Scan(&s.ChunkID, &s.DocumentID, &s.DocumentVersionID, &s.DocumentTitle, &s.VersionNumber, &s.ChunkIndex, &s.Content, &s.PageNumber, &s.SectionNumber, &s.SectionTitle, &s.Score); err != nil {
			return nil, mapError("read semantic result", err)
		}
		out = append(out, s)
	}
	return out, mapError("semantic results", rows.Err())
}
