package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaxChunks      = 2000
	MaxChunkBytes  = 64 << 10
	MaxChunksBytes = 8 << 20
)

type DocumentChunk struct {
	ID                uuid.UUID `json:"id"`
	DocumentVersionID uuid.UUID `json:"document_version_id"`
	ChunkIndex        int       `json:"chunk_index"`
	Content           string    `json:"content"`
	PageNumber        *int      `json:"page_number"`
	SectionNumber     *string   `json:"section_number"`
	SectionTitle      *string   `json:"section_title"`
	CreatedAt         time.Time `json:"created_at"`
}

// ValidateChunks is shared by the untrusted HTTP boundary and transactional
// persistence. Contents are never embedded in validation errors.
func ValidateChunks(chunks []DocumentChunk) error {
	if len(chunks) == 0 || len(chunks) > MaxChunks {
		return ErrInvalidInput
	}
	seen := make(map[int]bool, len(chunks))
	total := 0
	for _, c := range chunks {
		if c.ChunkIndex < 0 || c.ChunkIndex > 1_000_000 || seen[c.ChunkIndex] {
			return ErrInvalidInput
		}
		seen[c.ChunkIndex] = true
		if !validChunkText(c.Content, MaxChunkBytes) || strings.TrimSpace(c.Content) == "" {
			return ErrInvalidInput
		}
		total += len(c.Content)
		if total > MaxChunksBytes {
			return ErrInvalidInput
		}
		if c.PageNumber != nil && (*c.PageNumber < 1 || *c.PageNumber > 1_000_000) {
			return ErrInvalidInput
		}
		if c.SectionNumber != nil && !validChunkText(*c.SectionNumber, 128) {
			return ErrInvalidInput
		}
		if c.SectionTitle != nil && !validChunkText(*c.SectionTitle, 512) {
			return ErrInvalidInput
		}
	}
	return nil
}
func validChunkText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
