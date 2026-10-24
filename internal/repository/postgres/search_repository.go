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

// Used by both search and persisted chat citations; callers alias the chunk c.
const chunkMetadataSQL = `
-- A numbered instruction is not necessarily a heading. Correct only metadata
-- contradicted by the chunk's own leading number/text; never invent a title.
CROSS JOIN LATERAL (
    SELECT CASE WHEN c.content ~ '^\s*[0-9]+[.)]'
                THEN regexp_match(c.content, '^\s*([0-9]+(?:\.[0-9]+)*)(?:[.)])?\s+') END AS number
) prefix
CROSS JOIN LATERAL (
    SELECT coalesce(prefix.number[1], c.section_number) AS section_number,
           CASE
             WHEN prefix.number[1] IS NOT NULL
                  AND prefix.number[1] IS DISTINCT FROM c.section_number THEN NULL
             WHEN prefix.number[1] IS NOT NULL
                  AND btrim(regexp_replace(c.content, '^\s*[0-9]+(?:\.[0-9]+)*(?:[.)])?\s+', '')) = btrim(c.section_title)
                  AND (c.section_title ~ '[.!?;:](\s*\[[0-9]+\])?\s*$' OR length(c.section_title) > 120) THEN NULL
             ELSE c.section_title
           END AS section_title
) metadata`

// Both stages keep migration 5's exact GIN expression as their candidate
// predicate. Coverage/ranking is evaluated only on authorized matching chunks.
// A single statement gives both stages the same ACL/publication snapshot.
const searchFromSQL = `
FROM document_chunks c
JOIN document_versions v ON v.id = c.document_version_id
JOIN documents d ON d.id = v.document_id AND d.active_version_id = v.id
JOIN users u ON u.id = $1 AND u.is_active AND u.role = $2
CROSS JOIN search_query q
` + chunkMetadataSQL + `
CROSS JOIN LATERAL (
    SELECT to_tsvector('russian'::regconfig, c.content) AS body,
           setweight(to_tsvector('russian'::regconfig, c.content), 'A') ||
           setweight(to_tsvector('russian'::regconfig, coalesce(metadata.section_title, '')), 'B') ||
           setweight(to_tsvector('russian'::regconfig, d.title), 'C') AS weighted
) vectors
`

const searchVisibilitySQL = `
WHERE v.status = 'ready'
  AND (u.role = 'admin' OR EXISTS (
      SELECT 1 FROM document_access a
      WHERE a.document_id = d.id AND a.user_id = u.id
  ))`

const searchColumnsSQL = `c.id AS chunk_id, d.id AS document_id, v.id AS version_id,
       d.title, v.version_number, c.chunk_index, c.content, c.page_number,
       metadata.section_number, metadata.section_title, `

const searchOrderSQL = ` ORDER BY score DESC, document_id ASC, version_id ASC, chunk_index ASC, chunk_id ASC LIMIT $4`

// Count distinct concepts, not aliases or repeated occurrences. Expanding a
// word family must neither dilute coverage nor count one word twice.
const searchCoverageSQL = `(
    SELECT count(DISTINCT coalesce(f.word, body_word))
    FROM unnest(tsvector_to_array(vectors.body)) body_word
    LEFT JOIN lexeme_families f ON body_word = ANY(f.forms)
    WHERE coalesce(f.word, body_word) = ANY(q.lexemes)
)`

const searchSQL = `
WITH lexeme_families(word, forms) AS MATERIALIZED (
    -- Russian Snowball stemming does not equate these inflections/derivations.
    -- These are lexical equivalences, not document- or question-specific answers.
    VALUES ('соста', ARRAY['соста','состав']),
           ('контрол', ARRAY['контрол','контролир'])
), input AS MATERIALIZED (
    SELECT websearch_to_tsquery('russian'::regconfig, $3) AS strict_query,
           to_tsvector('russian'::regconfig, $3) AS words,
           -- Only generic question/duty framing is ignored, not domain topics
           -- such as salary, liability, vacation or a named actor.
           to_tsvector('russian'::regconfig,
               'какие какой каков какова каковы обязанность обязанности должен должна должны должно' ||
               CASE WHEN $3 ~* '\mдолж(ен|на|ны|но)\M' THEN ' делать' ELSE '' END ||
               CASE WHEN $3 ~* '^\s*за\s+что\s+отвеч[[:alpha:]]*\M' THEN ' отвечать' ELSE '' END ||
               CASE WHEN $3 ~* '\mвход[[:alpha:]]*\s+в\s+состав[[:alpha:]]*\M' THEN ' входить' ELSE '' END) AS framing
), terms AS MATERIALIZED (
    SELECT DISTINCT coalesce(f.word, raw_word) AS word,
           coalesce(f.forms, ARRAY[raw_word]) AS forms
    FROM input CROSS JOIN unnest(tsvector_to_array(words)) raw_word
    LEFT JOIN lexeme_families f ON raw_word = ANY(f.forms)
    WHERE NOT raw_word = ANY(tsvector_to_array(framing))
), search_query AS MATERIALIZED (
    SELECT input.*,
           -- Inspect parsed operators too, including exclusions after brackets.
           NOT ($3 LIKE '%"%' OR strict_query::text ~ '[!|<]') AS allow_relaxed,
           ARRAY(SELECT word FROM terms) AS lexemes,
           coalesce((SELECT string_agg(quote_literal(form), ' | ' ORDER BY form)
                     FROM terms CROSS JOIN unnest(forms) form), '')::tsquery AS relaxed_query
    FROM input
), strict_results AS MATERIALIZED (
    SELECT ` + searchColumnsSQL + `
           1.0 + ts_rank_cd(vectors.weighted, q.strict_query, 32) AS score
    ` + searchFromSQL + searchVisibilitySQL + `
      AND querytree(q.strict_query) <> 'T'
      AND to_tsvector('russian'::regconfig, c.content) @@ q.strict_query
      AND ts_rank_cd(vectors.weighted, q.strict_query, 32) > 0
    ` + searchOrderSQL + `
), relaxed_results AS (
    SELECT ` + searchColumnsSQL + `
           0.7 * ` + searchCoverageSQL + `::double precision / cardinality(q.lexemes)
           + 0.3 * ts_rank_cd(vectors.weighted, q.relaxed_query, 32) AS score
    ` + searchFromSQL + searchVisibilitySQL + `
      -- Three good strict sources suffice; never pad merely to fill LIMIT.
      AND (SELECT count(*) FROM strict_results) < least($4, 3)
      AND q.allow_relaxed AND cardinality(q.lexemes) >= 2
      AND to_tsvector('russian'::regconfig, c.content) @@ q.relaxed_query
      AND NOT EXISTS (SELECT 1 FROM strict_results s WHERE s.chunk_id = c.id)
      -- Unique meaningful lexemes, not term frequency. At least two and 80%
      -- must occur in the body itself: shared document titles cannot qualify
      -- unrelated clauses. Extra matches in headings/titles only boost rank.
      AND ` + searchCoverageSQL + ` >= greatest(2, ceil(0.8 * cardinality(q.lexemes)))
    ` + searchOrderSQL + `
), selected AS MATERIALIZED (
SELECT * FROM strict_results
UNION ALL
SELECT * FROM relaxed_results
` + searchOrderSQL + `
), table_context AS (
    -- The existing DOCX parser stores a caption and each table row separately.
    -- Include only directly contiguous tab-separated rows under a matched
    -- numbered caption. Keep real chunk IDs/text for citations and provenance.
    SELECT ` + searchColumnsSQL + `parent.score - 0.000001 * (c.chunk_index-parent.chunk_index) AS score
    ` + searchFromSQL + ` CROSS JOIN selected parent ` + searchVisibilitySQL + `
      AND q.allow_relaxed
      AND parent.content ~ ':\s*$' AND parent.section_number IS NOT NULL
      AND c.document_version_id = parent.version_id
      AND c.chunk_index > parent.chunk_index AND c.chunk_index <= parent.chunk_index + 8
      AND c.section_number = parent.section_number
      AND position(E'\t' IN c.content) > 0
      AND NOT EXISTS (SELECT 1 FROM selected s WHERE s.chunk_id = c.id)
      AND NOT EXISTS (
          SELECT 1 FROM document_chunks gap
          WHERE gap.document_version_id = parent.version_id
            AND gap.chunk_index > parent.chunk_index AND gap.chunk_index < c.chunk_index
            AND (position(E'\t' IN gap.content) = 0
                 OR gap.section_number IS DISTINCT FROM parent.section_number)
      )
)
SELECT * FROM selected
UNION ALL
SELECT * FROM table_context
` + searchOrderSQL

func (r *SearchRepository) Search(ctx context.Context, userID uuid.UUID, role domain.Role, query domain.SearchQuery) ([]domain.SearchResult, error) {
	return searchChunks(ctx, r.db, userID, role, query)
}

// SearchVariants keeps ACL and active-ready selection consistent across the
// whole plan. The read-only transaction contains only SQL, never an LLM call.
func (r *SearchRepository) SearchVariants(ctx context.Context, userID uuid.UUID, role domain.Role, queries []domain.SearchQuery) ([][]domain.SearchResult, error) {
	if userID == uuid.Nil || !role.Valid() {
		return nil, domain.ErrAccessDenied
	}
	if len(queries) < 1 || len(queries) > 2 {
		return nil, domain.ErrInvalidInput
	}
	for _, query := range queries {
		if _, err := query.Normalize(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, mapError("begin search plan", err)
	}
	defer tx.Rollback()
	lists := make([][]domain.SearchResult, 0, len(queries))
	for _, query := range queries {
		results, err := searchChunks(ctx, tx, userID, role, query)
		if err != nil {
			return nil, err
		}
		lists = append(lists, results)
	}
	if err := tx.Commit(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, mapError("commit search plan", err)
	}
	return lists, nil
}

type searchQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func searchChunks(ctx context.Context, db searchQuerier, userID uuid.UUID, role domain.Role, query domain.SearchQuery) ([]domain.SearchResult, error) {
	if userID == uuid.Nil || !role.Valid() {
		return nil, domain.ErrAccessDenied
	}
	query, err := query.Normalize()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, searchSQL, userID, role, query.Query, query.Limit)
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
