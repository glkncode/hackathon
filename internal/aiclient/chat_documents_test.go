package aiclient

import (
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDocumentInterpretationBoundary(t *testing.T) {
	id := uuid.New()
	req := DocumentInterpretRequest{Query: "Подготовь проект", Templates: []domain.ChatTemplateOption{{ID: id}}, Fields: []domain.TemplateField{{Key: "person", Type: "text"}}}
	valid := func() DocumentInterpretation {
		return DocumentInterpretation{Intent: "create_document", TemplateIDs: []string{id.String()}, Changes: map[string]json.RawMessage{}}
	}
	cases := map[string]func(*DocumentInterpretation){
		"unknown_intent":     func(r *DocumentInterpretation) { r.Intent = "shell" },
		"invented_template":  func(r *DocumentInterpretation) { r.TemplateIDs = []string{uuid.NewString()} },
		"duplicate_template": func(r *DocumentInterpretation) { r.TemplateIDs = append(r.TemplateIDs, id.String()) },
		"unknown_field": func(r *DocumentInterpretation) {
			r.Intent = "answer_field"
			r.Changes["hidden"] = json.RawMessage(`"x"`)
		},
		"question_mutates": func(r *DocumentInterpretation) {
			r.Intent = "knowledge_question"
			r.Changes["person"] = json.RawMessage(`"x"`)
		},
		"ambiguous_mutates": func(r *DocumentInterpretation) {
			r.Intent = "answer_field"
			r.NeedsClarification = true
			r.Changes["person"] = json.RawMessage(`"x"`)
		},
		"unknown_target": func(r *DocumentInterpretation) { r.TargetField = "hidden" },
		"null_changes":   func(r *DocumentInterpretation) { r.Changes = nil },
	}
	if err := valid().Validate(req); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			if r.Validate(req) == nil {
				t.Fatal("untrusted action accepted")
			}
		})
	}
}
func TestDocumentInterpreterPrivateHTTP(t *testing.T) {
	id := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/chat-documents/interpret" || r.Header.Get("X-Service-Token") != "secret-service-token" {
			t.Error("wrong private contract")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DocumentInterpretation{Intent: "knowledge_question", TemplateIDs: []string{}, Changes: map[string]json.RawMessage{}})
	}))
	defer server.Close()
	result, err := newTestClient(t, server.URL).InterpretDocument(context.Background(), DocumentInterpretRequest{Query: "Как оформить?", Templates: []domain.ChatTemplateOption{{ID: id}}})
	if err != nil || result.Intent != "knowledge_question" {
		t.Fatal(err)
	}
}
