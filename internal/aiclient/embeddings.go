package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type EmbeddingResponse struct {
	Model      string                   `json:"model"`
	Revision   string                   `json:"revision"`
	Dimensions int                      `json:"dimensions"`
	Format     string                   `json:"format_revision"`
	Embeddings [][]float64              `json:"embeddings"`
	Windows    []domain.EmbeddingWindow `json:"windows"`
}

func (r EmbeddingResponse) Validate(kind string, count int) error {
	invalid := &Error{Code: "invalid_embedding_response"}
	if (domain.EmbeddingIdentity{Model: r.Model, Revision: r.Revision, Dimensions: r.Dimensions, Format: r.Format}) != domain.E5Identity() ||
		len(r.Embeddings) != len(r.Windows) || len(r.Windows) == 0 || len(r.Windows) > 128 {
		return invalid
	}
	current := -1
	end := 0
	number := 0
	total := 0
	for i, w := range r.Windows {
		if w.InputIndex != current {
			if w.InputIndex != current+1 || (current >= 0 && end != total) {
				return invalid
			}
			current = w.InputIndex
			number = 0
			end = 0
			total = w.TotalTokens
		}
		if current >= count || w.WindowIndex != number || w.TotalTokens != total || total <= 0 || w.StartToken < 0 || w.EndToken <= w.StartToken || w.EndToken > total || w.EndToken-w.StartToken > 508 ||
			(number == 0 && w.StartToken != 0) || (number > 0 && w.StartToken != end-64) || domain.ValidateEmbedding(r.Embeddings[i]) != nil {
			return invalid
		}
		if kind == "query" && (number != 0 || w.EndToken != total) {
			return invalid
		}
		number++
		end = w.EndToken
	}
	if current != count-1 || end != total {
		return invalid
	}
	return nil
}

func (c *Client) privateJSON(ctx context.Context, path string, input, output any, maxResponse int64) error {
	invalid := &Error{Code: "invalid_ai_response"}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 300<<10 {
		return invalid
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/internal/v1/ingest") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return invalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: "ai_unavailable", Temporary: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rejectedResponse(resp)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	if int64(len(data)) > maxResponse {
		return invalid
	}
	if exactJSON(data) != nil {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}
func (c *Client) Embed(ctx context.Context, kind string, inputs []string) (EmbeddingResponse, error) {
	if (kind != "query" && kind != "passage") || len(inputs) == 0 || len(inputs) > 8 {
		return EmbeddingResponse{}, domain.ErrInvalidInput
	}
	total := 0
	for _, s := range inputs {
		total += len(s)
		if !validGenerateText(s, 66048, 66048) {
			return EmbeddingResponse{}, domain.ErrInvalidInput
		}
	}
	if total > 256<<10 {
		return EmbeddingResponse{}, domain.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, c.embeddingTimeout)
	defer cancel()
	var result EmbeddingResponse
	err := c.privateJSON(ctx, "/internal/v1/embeddings", struct {
		Kind   string   `json:"kind"`
		Inputs []string `json:"inputs"`
	}{kind, inputs}, &result, 2<<20)
	if err == nil {
		err = result.Validate(kind, len(inputs))
	}
	return result, err
}
func (c *Client) Evidence(ctx context.Context, query string, sources []domain.SearchResult) ([]uuid.UUID, error) {
	// Bound the evidence context without truncating a source or fabricating IDs.
	req := GenerateRequest{Query: query, History: []GenerateMessage{}, Sources: []GenerateSource{}}
	size := len(query)
	allowed := map[uuid.UUID]string{}
	for _, s := range sources {
		cost := len(s.Content) + len(s.DocumentTitle) + 512
		if size+cost > 48<<10 || len([]rune(s.Content)) > 16000 {
			continue
		}
		size += cost
		allowed[s.ChunkID] = s.Content
		req.Sources = append(req.Sources, GenerateSource{ChunkID: s.ChunkID, DocumentTitle: s.DocumentTitle, VersionNumber: s.VersionNumber, Content: s.Content, SectionNumber: s.SectionNumber, PageNumber: s.PageNumber})
	}
	if len(req.Sources) == 0 {
		return []uuid.UUID{}, nil
	}
	var result struct {
		Evidence []struct {
			ChunkID uuid.UUID `json:"chunk_id"`
			Quote   string    `json:"quote"`
		} `json:"evidence"`
	}
	ctx, cancel := context.WithTimeout(ctx, c.generateTimeout)
	defer cancel()
	if err := c.privateJSON(ctx, "/internal/v1/evidence", req, &result, 128<<10); err != nil {
		return nil, err
	}
	if result.Evidence == nil || len(result.Evidence) > len(req.Sources) {
		return nil, &Error{Code: "invalid_evidence_response"}
	}
	seen := map[uuid.UUID]bool{}
	ids := []uuid.UUID{}
	for _, e := range result.Evidence {
		content, ok := allowed[e.ChunkID]
		canonicalQuote := strings.Join(strings.Fields(e.Quote), " ")
		// PDF extraction preserves NBSP and line breaks. Only whitespace may
		// differ; words, numbers, punctuation and negations must match verbatim.
		if !ok || seen[e.ChunkID] || len([]rune(canonicalQuote)) < 8 || len([]rune(e.Quote)) > 4000 || !strings.Contains(strings.Join(strings.Fields(content), " "), canonicalQuote) {
			return nil, &Error{Code: "invalid_evidence_response"}
		}
		seen[e.ChunkID] = true
		ids = append(ids, e.ChunkID)
	}
	return ids, nil
}
