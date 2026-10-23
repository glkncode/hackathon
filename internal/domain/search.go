package domain

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 30
	MaxSearchRunes     = 500
)

type SearchQuery struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Normalize applies the same bounds at the service and repository boundaries.
// Zero means the default limit; negative or excessive limits are errors.
func (q SearchQuery) Normalize() (SearchQuery, error) {
	if !utf8.ValidString(q.Query) || utf8.RuneCountInString(q.Query) > MaxSearchRunes || strings.ContainsRune(q.Query, 0) {
		return SearchQuery{}, ErrInvalidInput
	}
	q.Query = strings.TrimSpace(q.Query)
	if q.Limit == 0 {
		q.Limit = DefaultSearchLimit
	}
	if q.Query == "" || q.Limit < 1 || q.Limit > MaxSearchLimit {
		return SearchQuery{}, ErrInvalidInput
	}
	return q, nil
}

// SearchResult is a citation projection, not a stored chunk: it combines the
// chunk identity/content with its authorized document and exact active version.
// Score is PostgreSQL relevance, never a probability or confidence percentage.
type SearchResult struct {
	ChunkID           uuid.UUID `json:"chunk_id"`
	DocumentID        uuid.UUID `json:"document_id"`
	DocumentVersionID uuid.UUID `json:"document_version_id"`
	DocumentTitle     string    `json:"document_title"`
	VersionNumber     int       `json:"version_number"`
	ChunkIndex        int       `json:"chunk_index"`
	Content           string    `json:"content"`
	PageNumber        *int      `json:"page_number"`
	SectionNumber     *string   `json:"section_number"`
	SectionTitle      *string   `json:"section_title"`
	Score             float64   `json:"score"`
}
