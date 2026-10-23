package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

func generateInput() GenerateRequest {
	page, section := 13, "4.6"
	return GenerateRequest{Query: "секретный вопрос", History: []GenerateMessage{{Role: domain.ChatRoleUser, Content: "предыдущий вопрос"}}, Sources: []GenerateSource{{ChunkID: uuid.New(), DocumentTitle: "Инструкция", VersionNumber: 1, PageNumber: &page, SectionNumber: &section, Content: "секретный фрагмент"}}}
}
func generatedBody(id uuid.UUID) string {
	return `{"status":"answered","answer":"Согласуйте документ.","used_source_ids":["` + id.String() + `"],"clarification_question":null}`
}

func TestGenerateContractAndStatusValidation(t *testing.T) {
	input := generateInput()
	for _, tc := range []struct {
		name, body string
		status     GenerateStatus
	}{
		{"answered", generatedBody(input.Sources[0].ChunkID), GenerateAnswered},
		{"clarification", `{"status":"needs_clarification","answer":null,"used_source_ids":[],"clarification_question":"Какой вид документа?"}`, GenerateNeedsClarification},
		{"insufficient", `{"status":"insufficient_information","answer":null,"used_source_ids":[],"clarification_question":null}`, GenerateInsufficientInformation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/internal/v1/rag/generate" || r.Header.Get("X-Service-Token") != "secret-service-token" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "" {
					t.Error("private RAG contract mismatch")
				}
				body, _ := io.ReadAll(r.Body)
				var wire map[string]json.RawMessage
				if err := json.Unmarshal(body, &wire); err != nil || len(wire) != 3 || wire["query"] == nil || wire["history"] == nil || wire["sources"] == nil {
					t.Error("unexpected identity/credentials in payload")
				}
				var got GenerateRequest
				if err := json.Unmarshal(body, &got); err != nil || got.Sources[0].ChunkID != input.Sources[0].ChunkID || *got.Sources[0].PageNumber != 13 || *got.Sources[0].SectionNumber != "4.6" {
					t.Error("citation metadata changed")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got, err := newTestClient(t, server.URL).Generate(context.Background(), input)
			if err != nil || got.Status != tc.status {
				t.Fatal(got, err)
			}
		})
	}
}

func TestGenerateRejectsUntrustedResponses(t *testing.T) {
	input := generateInput()
	good := generatedBody(input.Sources[0].ChunkID)
	bodies := map[string]string{
		"unknown source":      generatedBody(uuid.New()),
		"duplicate source":    strings.Replace(good, `"used_source_ids":["`+input.Sources[0].ChunkID.String()+`"]`, `"used_source_ids":["`+input.Sources[0].ChunkID.String()+`","`+input.Sources[0].ChunkID.String()+`"]`, 1),
		"no citations":        strings.Replace(good, input.Sources[0].ChunkID.String(), uuid.Nil.String(), 1),
		"missing field":       strings.Replace(good, `,"clarification_question":null`, "", 1),
		"unknown field":       strings.Replace(good, `"clarification_question":null`, `"clarification_question":null,"command":"delete database"`, 1),
		"duplicate field":     strings.Replace(good, `"status":"answered"`, `"status":"search","status":"answered"`, 1),
		"case folded":         strings.Replace(good, `"status"`, `"Status"`, 1),
		"null citations":      strings.Replace(good, `["`+input.Sources[0].ChunkID.String()+`"]`, `null`, 1),
		"blank answer":        strings.Replace(good, "Согласуйте документ.", "  ", 1),
		"NUL answer":          strings.Replace(good, "Согласуйте документ.", `bad\u0000text`, 1),
		"large answer":        strings.Replace(good, "Согласуйте документ.", strings.Repeat("я", domain.MaxChatAssistantRunes+1), 1),
		"invalid status":      strings.Replace(good, `"answered"`, `"tool_call"`, 1),
		"inconsistent fields": strings.Replace(good, `"clarification_question":null`, `"clarification_question":"private source"`, 1),
		"bad JSON":            `{"secret-service-token":`, "null": `null`, "trailing": good + ` {}`,
		"body limit": strings.Repeat(" ", MaxGenerateResponseBytes+1), "UTF8": string([]byte{255}),
		"nested": strings.Repeat("[", 33) + `null` + strings.Repeat("]", 33),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Generate(context.Background(), input)
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != "invalid_ai_response" {
				t.Fatal("invalid response accepted", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("unsafe error")
			}
		})
	}
}

func TestGenerateHTTPFailuresAndCancellation(t *testing.T) {
	for _, status := range []int{404, 401, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, "private body secret-service-token")
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Generate(context.Background(), generateInput())
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != "ai_rejected" {
				t.Fatal(err)
			}
		})
	}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	if _, err := newTestClient(t, redirect.URL).Generate(context.Background(), generateInput()); err == nil || hits.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
	wrongType := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, generatedBody(uuid.New()))
	}))
	defer wrongType.Close()
	if _, err := newTestClient(t, wrongType.URL).Generate(context.Background(), generateInput()); err == nil {
		t.Fatal("non-JSON accepted")
	}
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			client := newTestClient(t, server.URL)
			go func() { _, err := client.Generate(ctx, generateInput()); done <- err }()
			<-started
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if timeout {
					var remote *Error
					if !errors.As(err, &remote) || remote.Code != "ai_timeout" {
						t.Fatal(err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("RAG request did not stop")
			}
		})
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	endpoint := closed.URL
	closed.Close()
	if _, err := newTestClient(t, endpoint).Generate(context.Background(), generateInput()); err == nil {
		t.Fatal("network error ignored")
	}
}

func TestGenerateRequestBounds(t *testing.T) {
	input := generateInput()
	input.Sources[0].Content = strings.Repeat("x", domain.MaxChunkBytes+1)
	if _, err := input.ContextBytes(); err == nil {
		t.Fatal("oversize chunk accepted")
	}
	input = generateInput()
	input.History = make([]GenerateMessage, MaxGenerateHistory+1)
	if _, err := input.ContextBytes(); err == nil {
		t.Fatal("unbounded history")
	}
	input = generateInput()
	input.Sources = nil
	if _, err := input.ContextBytes(); err == nil {
		t.Fatal("empty retrieval accepted")
	}
	input = generateInput()
	input.Sources = []GenerateSource{}
	for i := 0; i < 3; i++ {
		s := generateInput().Sources[0]
		s.Content = strings.Repeat("x", domain.MaxChunkBytes)
		input.Sources = append(input.Sources, s)
	}
	if _, err := input.ContextBytes(); err == nil {
		t.Fatal("unbounded total context")
	}
}
