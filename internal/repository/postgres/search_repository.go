package postgres

import (
	"context"
	"database/sql"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

const searchTimeout = 5 * time.Second

type SearchRepository struct{ db *sql.DB }

func NewSearchRepository(db *sql.DB) *SearchRepository { return &SearchRepository{db: db} }

// Keep the tsvector expression identical to migration 5's existing GIN index.
// All visibility predicates run in the same statement/snapshot as matching,
// ranking and LIMIT. No unauthorized content or count is fetched into Go.
const searchSQL = `
WITH search_query AS (
    SELECT websearch_to_tsquery('russian'::regconfig, $3) AS query
)
SELECT c.id, d.id, v.id, d.title, v.version_number, c.chunk_index,
       c.content, c.page_number, c.section_number, c.section_title,
       ts_rank_cd(to_tsvector('russian'::regconfig, c.content), q.query) AS score
FROM document_chunks c
JOIN document_versions v ON v.id = c.document_version_id
JOIN documents d ON d.id = v.document_id AND d.active_version_id = v.id
JOIN users u ON u.id = $1 AND u.is_active AND u.role = $2
CROSS JOIN search_query q
WHERE v.status = 'ready'
  AND (u.role = 'admin' OR EXISTS (
      SELECT 1 FROM document_access a
      WHERE a.document_id = d.id AND a.user_id = u.id
  ))
  AND to_tsvector('russian'::regconfig, c.content) @@ q.query
ORDER BY score DESC, d.id ASC, v.id ASC, c.chunk_index ASC, c.id ASC
LIMIT $4`

func (r *SearchRepository) Search(ctx context.Context, userID uuid.UUID, role domain.Role, query domain.SearchQuery) ([]domain.SearchResult, error) {
	if userID == uuid.Nil || !role.Valid() {
		return nil, domain.ErrAccessDenied
	}
	query, err := query.Normalize()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, searchSQL, userID, role, query.Query, query.Limit)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, mapError("search documents", err)
	}
	defer rows.Close()
	results := make([]domain.SearchResult, 0)
	for rows.Next() {
		var result domain.SearchResult
		if err := rows.Scan(&result.ChunkID, &result.DocumentID, &result.DocumentVersionID,
			&result.DocumentTitle, &result.VersionNumber, &result.ChunkIndex, &result.Content,
			&result.PageNumber, &result.SectionNumber, &result.SectionTitle, &result.Score); err != nil {
			return nil, mapError("read search result", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, mapError("read search results", err)
	}
	return results, nil
}
