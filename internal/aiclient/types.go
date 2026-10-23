package aiclient

import (
	"io"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

// Request transfers ownership of File to Ingest, including on failure.
// File must support concurrent Close to interrupt a blocked Read (as os.File does).
type Request struct {
	DocumentID        uuid.UUID
	DocumentVersionID uuid.UUID
	MIMEType          string
	Filename          string
	File              io.ReadCloser
}

type Response struct {
	DocumentVersionID uuid.UUID
	Chunks            []domain.DocumentChunk
	Warnings          []string
}

// Error contains only safe codes; never a URL, token, body or source content.
type Error struct {
	Code      string
	Temporary bool
}

func (e *Error) Error() string { return e.Code }

const MaxResponseBytes = 16 << 20
