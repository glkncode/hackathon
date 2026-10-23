package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

const (
	MaxGenerateContextBytes  = 128 << 10
	MaxGenerateRequestBytes  = 1 << 20
	MaxGenerateResponseBytes = 64 << 10
	MaxGenerateSources       = 30
	MaxGenerateHistory       = 50
)

type GenerateMessage struct {
	Role    domain.ChatRole `json:"role"`
	Content string          `json:"content"`
}

// This intentionally small projection contains neither identity nor storage or
// application credentials. Source and history text are untrusted data, not tools.
type GenerateSource struct {
	ChunkID       uuid.UUID `json:"chunk_id"`
	DocumentTitle string    `json:"document_title"`
	VersionNumber int       `json:"version_number"`
	SectionNumber *string   `json:"section_number"`
	PageNumber    *int      `json:"page_number"`
	Content       string    `json:"content"`
}

type GenerateRequest struct {
	Query   string            `json:"query"`
	History []GenerateMessage `json:"history"`
	Sources []GenerateSource  `json:"sources"`
}

type GenerateStatus string

const (
	GenerateAnswered                GenerateStatus = "answered"
	GenerateNeedsClarification      GenerateStatus = "needs_clarification"
	GenerateInsufficientInformation GenerateStatus = "insufficient_information"
)

type GenerateResponse struct {
	Status                GenerateStatus `json:"status"`
	Answer                *string        `json:"answer"`
	UsedSourceIDs         []uuid.UUID    `json:"used_source_ids"`
	ClarificationQuestion *string        `json:"clarification_question"`
}

func validGenerateText(value string, maxBytes, maxRunes int) bool {
	return utf8.ValidString(value) && len(value) <= maxBytes && utf8.RuneCountInString(value) <= maxRunes && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) != ""
}

// ContextBytes also validates bounds before allocating or sending JSON. Metadata
// counts toward the budget, as do user questions and assistant history.
func (r GenerateRequest) ContextBytes() (int, error) {
	invalid := &Error{Code: "invalid_rag_request"}
	if _, err := (domain.SearchQuery{Query: r.Query}).Normalize(); err != nil || len(r.Sources) < 1 || len(r.Sources) > MaxGenerateSources || len(r.History) > MaxGenerateHistory {
		return 0, invalid
	}
	size := len(r.Query)
	seen := map[uuid.UUID]bool{}
	for _, s := range r.Sources {
		if s.ChunkID == uuid.Nil || seen[s.ChunkID] || s.VersionNumber < 1 || !validGenerateText(s.Content, domain.MaxChunkBytes, domain.MaxChunkBytes) || !validGenerateText(s.DocumentTitle, 4096, 4096) || (s.PageNumber != nil && (*s.PageNumber < 1 || *s.PageNumber > 1_000_000)) {
			return 0, invalid
		}
		seen[s.ChunkID] = true
		size += len(s.Content) + len(s.DocumentTitle)
		if s.SectionNumber != nil {
			if !utf8.ValidString(*s.SectionNumber) || len(*s.SectionNumber) > 128 || strings.ContainsRune(*s.SectionNumber, 0) {
				return 0, invalid
			}
			size += len(*s.SectionNumber)
		}
	}
	for _, h := range r.History {
		if (h.Role != domain.ChatRoleUser && h.Role != domain.ChatRoleAssistant) || !validGenerateText(h.Content, domain.MaxChatAssistantBytes, domain.MaxChatAssistantRunes) {
			return 0, invalid
		}
		size += len(h.Content)
	}
	if size > MaxGenerateContextBytes {
		return 0, invalid
	}
	return size, nil
}

// Validate is repeated by ChatService, so alternate generators cannot bypass the
// allowlist or turn model output into application actions or invented citations.
func (r GenerateResponse) Validate(request GenerateRequest) error {
	invalid := &Error{Code: "invalid_ai_response"}
	if r.UsedSourceIDs == nil {
		return invalid
	}
	allowed := map[uuid.UUID]bool{}
	used := map[uuid.UUID]bool{}
	for _, s := range request.Sources {
		allowed[s.ChunkID] = true
	}
	for _, id := range r.UsedSourceIDs {
		if id == uuid.Nil || !allowed[id] || used[id] {
			return invalid
		}
		used[id] = true
	}
	valid := func(s *string) bool {
		return s != nil && validGenerateText(*s, domain.MaxChatAssistantBytes, domain.MaxChatAssistantRunes)
	}
	empty := func(s *string) bool { return s == nil || *s == "" }
	switch r.Status {
	case GenerateAnswered:
		if !valid(r.Answer) || !empty(r.ClarificationQuestion) || len(r.UsedSourceIDs) == 0 {
			return invalid
		}
	case GenerateNeedsClarification:
		if !valid(r.ClarificationQuestion) || !empty(r.Answer) {
			return invalid
		}
	case GenerateInsufficientInformation:
		if !empty(r.Answer) || !empty(r.ClarificationQuestion) || len(r.UsedSourceIDs) != 0 {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

// exactJSON rejects duplicate keys as well as malformed/trailing JSON. A plain
// Decoder.DisallowUnknownFields alone still accepts duplicated or case-folded keys.
func exactJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	depth := 0
	var value func() error
	value = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 32 {
			return errors.New("JSON nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := t.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return errors.New("invalid JSON object")
					}
					seen[name] = true
					if err := value(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := value(); err != nil {
						return err
					}
				}
			default:
				return errors.New("invalid JSON delimiter")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeGenerate(body []byte, request GenerateRequest) (GenerateResponse, error) {
	invalid := &Error{Code: "invalid_ai_response"}
	if !utf8.Valid(body) || exactJSON(body) != nil {
		return GenerateResponse{}, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != 4 {
		return GenerateResponse{}, invalid
	}
	for _, key := range []string{"status", "answer", "used_source_ids", "clarification_question"} {
		if _, ok := fields[key]; !ok {
			return GenerateResponse{}, invalid
		}
	}
	var response GenerateResponse
	if json.Unmarshal(body, &response) != nil || response.Validate(request) != nil {
		return GenerateResponse{}, invalid
	}
	return response, nil
}

func (c *Client) Generate(ctx context.Context, input GenerateRequest) (GenerateResponse, error) {
	if _, err := input.ContextBytes(); err != nil {
		return GenerateResponse{}, err
	}
	if input.History == nil {
		input.History = []GenerateMessage{}
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > MaxGenerateRequestBytes {
		return GenerateResponse{}, &Error{Code: "invalid_rag_request"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.generateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.generateEndpoint, bytes.NewReader(body))
	if err != nil {
		return GenerateResponse{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-Token", c.token)
	resp, err := c.http.Do(req)
	classify := func() (GenerateResponse, error) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GenerateResponse{}, &Error{Code: "ai_timeout", Temporary: true}
		}
		if ctx.Err() != nil {
			return GenerateResponse{}, ctx.Err()
		}
		return GenerateResponse{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	if err != nil {
		return classify()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return GenerateResponse{}, &Error{Code: "ai_rejected", Temporary: resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return GenerateResponse{}, &Error{Code: "invalid_ai_response"}
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, MaxGenerateResponseBytes+1))
	if err != nil {
		return classify()
	}
	if len(body) > MaxGenerateResponseBytes {
		return GenerateResponse{}, &Error{Code: "invalid_ai_response"}
	}
	return decodeGenerate(body, input)
}
