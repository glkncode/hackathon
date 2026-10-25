package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"ai-assistant/internal/domain"
)

type ResolveRequest struct {
	Query     string            `json:"query"`
	History   []GenerateMessage `json:"history"`
	TimeoutMS int               `json:"timeout_ms"`
}
type QueryPart struct {
	MessageIndex int    `json:"message_index"`
	Quote        string `json:"quote"`
}
type ResolveResponse struct {
	Status                string        `json:"status"`
	Subject               *QueryPart    `json:"subject"`
	Queries               [][]QueryPart `json:"queries"`
	ClarificationQuestion *string       `json:"clarification_question"`
	LLMCalls              int           `json:"llm_calls"`
	Alternatives          []QueryPart   `json:"alternatives"`
	ClarificationKind     string        `json:"clarification_kind"`
}

// UnmarshalJSON rejects missing/null anchor indexes: int's zero value must not
// silently turn malformed input into a reference to history[0].
func (p *QueryPart) UnmarshalJSON(data []byte) error {
	var wire struct {
		MessageIndex *int    `json:"message_index"`
		Quote        *string `json:"quote"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&wire) != nil || dec.Decode(new(any)) != io.EOF || wire.MessageIndex == nil || wire.Quote == nil {
		return errors.New("invalid resolver anchor")
	}
	if *wire.MessageIndex < -1 || *wire.MessageIndex > 15 || !validGenerateText(*wire.Quote, 2000, 500) {
		return errors.New("invalid resolver anchor")
	}
	p.MessageIndex, p.Quote = *wire.MessageIndex, *wire.Quote
	return nil
}

const MissingSubjectQuestion = "О каком предмете или процессе вы спрашиваете?"

// Clarification is rendered from validated data on both sides of the boundary.
// Never pass through a model-authored question with ungrounded alternatives.
func (r ResolveResponse) Clarification(req ResolveRequest) (string, error) {
	invalid := &Error{Code: "invalid_resolver_response"}
	if r.Status != "needs_clarification" || r.Queries == nil || len(r.Queries) != 0 || r.Subject != nil || r.ClarificationQuestion == nil {
		return "", invalid
	}
	question := ""
	switch r.ClarificationKind {
	case "missing_subject":
		if len(req.History) != 0 || len(r.Alternatives) != 0 {
			return "", invalid
		}
		question = MissingSubjectQuestion
	case "choice":
		if len(r.Alternatives) != 2 {
			return "", invalid
		}
		for _, part := range r.Alternatives {
			text := req.Query
			if part.MessageIndex != -1 {
				if part.MessageIndex < 0 || part.MessageIndex >= len(req.History) {
					return "", invalid
				}
				text = req.History[part.MessageIndex].Content
			}
			if !validGenerateText(part.Quote, 2000, 500) || !strings.Contains(text, part.Quote) {
				return "", invalid
			}
		}
		if strings.EqualFold(r.Alternatives[0].Quote, r.Alternatives[1].Quote) {
			return "", invalid
		}
		question = "Вы спрашиваете о «" + r.Alternatives[0].Quote + "» или о «" + r.Alternatives[1].Quote + "»?"
	default:
		return "", invalid
	}
	if !validGenerateText(question, 2000, 500) || *r.ClarificationQuestion != question {
		return "", invalid
	}
	return question, nil
}

// Queries are reconstructed from exact input spans on BOTH sides of the private
// boundary. The model cannot supply document IDs, new entities or search facts.
func (r ResolveResponse) SearchQueries(req ResolveRequest) ([]string, error) {
	invalid := &Error{Code: "invalid_resolver_response"}
	if r.Queries == nil {
		return nil, invalid
	}
	if r.Status == "needs_clarification" {
		if _, err := r.Clarification(req); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if len(r.Alternatives) != 0 || r.ClarificationKind != "" {
		return nil, invalid
	}
	if (r.Status != "independent" && r.Status != "resolved") || r.ClarificationQuestion != nil || len(r.Queries) < 1 || len(r.Queries) > 3 {
		return nil, invalid
	}
	if r.Status == "resolved" {
		if r.Subject == nil || r.Subject.MessageIndex < 0 || r.Subject.MessageIndex >= len(req.History) || strings.TrimSpace(r.Subject.Quote) == "" || !strings.Contains(req.History[r.Subject.MessageIndex].Content, r.Subject.Quote) {
			return nil, invalid
		}
	} else if r.Subject != nil {
		return nil, invalid
	}
	out := []string{}
	primaryHistoryUsed, currentKept := false, false
	for qi, parts := range r.Queries {
		if qi == 0 && r.Subject != nil {
			found := false
			for _, part := range parts {
				if part == *r.Subject {
					found = true
				}
			}
			if !found {
				parts = append([]QueryPart{*r.Subject}, parts...)
			}
		}
		if len(parts) < 1 || len(parts) > 8 {
			return nil, invalid
		}
		words := []string{}
		for _, p := range parts {
			text := req.Query
			if p.MessageIndex != -1 {
				if p.MessageIndex < 0 || p.MessageIndex >= len(req.History) || r.Status == "independent" {
					return nil, invalid
				}
				text = req.History[p.MessageIndex].Content
				if qi == 0 {
					primaryHistoryUsed = true
				}
			} else if qi == 0 && p.Quote == req.Query {
				currentKept = true
			}
			if strings.TrimSpace(p.Quote) == "" || !strings.Contains(text, p.Quote) {
				return nil, invalid
			}
			words = append(words, p.Quote)
		}
		q, err := (domain.SearchQuery{Query: strings.Join(words, " ")}).Normalize()
		if err != nil {
			return nil, invalid
		}
		out = append(out, q.Query)
	}
	if !currentKept || (r.Status == "resolved" && !primaryHistoryUsed) {
		return nil, invalid
	}
	return out, nil
}

func (c *Client) Resolve(ctx context.Context, input ResolveRequest) (ResolveResponse, error) {
	invalid := &Error{Code: "invalid_resolver_request"}
	if _, err := (domain.SearchQuery{Query: input.Query}).Normalize(); err != nil || len(input.History) > 16 || input.TimeoutMS < 100 || input.TimeoutMS > 30000 {
		return ResolveResponse{}, invalid
	}
	size := len(input.Query)
	for _, h := range input.History {
		if (h.Role != domain.ChatRoleUser && h.Role != domain.ChatRoleAssistant) || !validGenerateText(h.Content, 16000, 4000) {
			return ResolveResponse{}, invalid
		}
		size += len(h.Content)
	}
	if size > 16<<10 || !utf8.ValidString(input.Query) {
		return ResolveResponse{}, invalid
	}
	var result ResolveResponse
	err := c.privateJSON(ctx, "/internal/v1/rag/resolve", input, &result, 16<<10)
	if remote, ok := err.(*Error); ok {
		result.LLMCalls = remote.LLMCalls
	}
	if result.LLMCalls < 0 || result.LLMCalls > 2 || (err == nil && result.LLMCalls == 0) {
		return ResolveResponse{}, &Error{Code: "invalid_resolver_response"}
	}
	if err == nil {
		_, err = result.SearchQueries(input)
	}
	return result, err
}
