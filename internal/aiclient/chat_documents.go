package aiclient

import (
	"ai-assistant/internal/domain"
	"bytes"
	"context"
	"encoding/json"
)

type DocumentInterpretRequest struct {
	Query         string                      `json:"query"`
	Stage         string                      `json:"stage"`
	Templates     []domain.ChatTemplateOption `json:"templates"`
	Fields        []domain.TemplateField      `json:"fields"`
	Values        map[string]json.RawMessage  `json:"values"`
	ExpectedField string                      `json:"expected_field"`
}
type DocumentInterpretation struct {
	Intent             string                     `json:"intent"`
	TemplateIDs        []string                   `json:"template_ids"`
	Changes            map[string]json.RawMessage `json:"changes"`
	TargetField        string                     `json:"target_field"`
	NeedsClarification bool                       `json:"needs_clarification"`
}

func (r *DocumentInterpretation) UnmarshalJSON(data []byte) error {
	type wire DocumentInterpretation
	var properties map[string]json.RawMessage
	if json.Unmarshal(data, &properties) != nil || len(properties) != 5 {
		return &Error{Code: "invalid_document_interpretation"}
	}
	for _, key := range []string{"intent", "template_ids", "changes", "target_field", "needs_clarification"} {
		raw, ok := properties[key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return &Error{Code: "invalid_document_interpretation"}
		}
	}
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = DocumentInterpretation(decoded)
	return nil
}

func (r DocumentInterpretation) Validate(req DocumentInterpretRequest) error {
	invalid := &Error{Code: "invalid_document_interpretation"}
	switch r.Intent {
	case "knowledge_question", "create_document", "continue_document", "edit_document_field", "answer_field", "show_document_progress", "validate_document", "generate_document", "pause_document", "cancel_document_flow", "ambiguous", "template_catalog":
	default:
		return invalid
	}
	if r.TemplateIDs == nil || r.Changes == nil || len(r.TemplateIDs) > 3 || len(r.Changes) > 64 || (r.NeedsClarification && len(r.Changes) > 0) {
		return invalid
	}
	ids := map[string]bool{}
	for _, t := range req.Templates {
		ids[t.ID.String()] = true
	}
	seen := map[string]bool{}
	for _, id := range r.TemplateIDs {
		if !ids[id] || seen[id] {
			return invalid
		}
		seen[id] = true
	}
	fields := map[string]bool{}
	for _, f := range req.Fields {
		fields[f.Key] = true
	}
	if r.TargetField != "" && !fields[r.TargetField] {
		return invalid
	}
	for k := range r.Changes {
		if !fields[k] {
			return invalid
		}
	}
	if len(r.Changes) > 0 && r.Intent != "answer_field" && r.Intent != "edit_document_field" {
		return invalid
	}
	return nil
}
func (c *Client) InterpretDocument(ctx context.Context, req DocumentInterpretRequest) (DocumentInterpretation, error) {
	var r DocumentInterpretation
	err := c.privateJSON(ctx, "/internal/v1/chat-documents/interpret", req, &r, 80<<10)
	if err == nil {
		err = r.Validate(req)
	}
	return r, err
}
