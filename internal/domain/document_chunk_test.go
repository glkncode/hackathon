package domain

import (
	"strings"
	"testing"
)

func TestChunkLimits(t *testing.T) {
	text := strings.Repeat("x", MaxChunkBytes)
	chunks := make([]DocumentChunk, MaxChunksBytes/MaxChunkBytes)
	for i := range chunks {
		chunks[i] = DocumentChunk{ChunkIndex: i, Content: text}
	}
	if err := ValidateChunks(chunks); err != nil {
		t.Fatal("valid limit rejected")
	}
	chunks = append(chunks, DocumentChunk{ChunkIndex: len(chunks), Content: "x"})
	if ValidateChunks(chunks) == nil {
		t.Fatal("total content limit missing")
	}
	title := strings.Repeat("x", 513)
	if ValidateChunks([]DocumentChunk{{Content: "x", SectionTitle: &title}}) == nil {
		t.Fatal("section title limit missing")
	}
	if ValidateChunks([]DocumentChunk{{Content: string([]byte{0xff})}}) == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
