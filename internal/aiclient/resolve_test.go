package aiclient

import (
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveAnchorsAndPrivateContract(t *testing.T) {
	req := ResolveRequest{Query: "А когда его вернуть?", History: []GenerateMessage{{Role: domain.ChatRoleUser, Content: "Как получить прибор?"}}, TimeoutMS: 1000}
	for _, mode := range []string{"valid", "invented", "index", "lost_current", "independent", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			result := ResolveResponse{Status: "resolved", LLMCalls: 1, Subject: &QueryPart{MessageIndex: 0, Quote: "прибор"}, Queries: [][]QueryPart{{{0, "прибор"}, {-1, req.Query}}}}
			switch mode {
			case "invented":
				result.Queries[0][0].Quote = "Зета 2027"
			case "index":
				result.Queries[0][0].MessageIndex = 4
			case "lost_current":
				result.Queries[0] = result.Queries[0][:1]
			case "independent":
				result.Status = "independent"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/internal/v1/rag/resolve" || r.Header.Get("X-Service-Token") != "private" {
					t.Error("private contract")
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "duplicate" {
					w.Write([]byte(`{"status":"independent","status":"resolved","queries":[],"clarification_question":null}`))
					return
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			client, err := New(Config{URL: server.URL, Token: "private", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			out, err := client.Resolve(context.Background(), req)
			if mode != "valid" {
				if err == nil {
					t.Fatal("unsafe plan accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			queries, err := out.SearchQueries(req)
			if err != nil || queries[0] != "прибор А когда его вернуть?" {
				t.Fatal(queries, err)
			}
		})
	}
}

func TestClarificationRequiresDistinctInputAlternativesForExistingTopic(t *testing.T) {
	req := ResolveRequest{Query: "When do I return it?", History: []GenerateMessage{{Role: domain.ChatRoleUser, Content: "object A and object B"}}}
	question := "Вы спрашиваете о «object A» или о «object B»?"
	plan := ResolveResponse{Status: "needs_clarification", ClarificationKind: "choice", Queries: [][]QueryPart{}, ClarificationQuestion: &question}
	if _, err := plan.SearchQueries(req); err == nil {
		t.Fatal("unjustified clarification accepted")
	}
	plan.Alternatives = []QueryPart{{MessageIndex: 0, Quote: "object A"}, {MessageIndex: 0, Quote: "object B"}}
	if _, err := plan.SearchQueries(req); err != nil {
		t.Fatal(err)
	}
	plan.Alternatives[1].Quote = "invented"
	if _, err := plan.SearchQueries(req); err == nil {
		t.Fatal("invented alternative accepted")
	}
	plan.Alternatives[1] = plan.Alternatives[0]
	if _, err := plan.SearchQueries(req); err == nil {
		t.Fatal("duplicate alternative accepted")
	}
}

func TestResolverClarificationCannotSmuggleInventedAlternatives(t *testing.T) {
	req := ResolveRequest{Query: "Как оформить выдачу установки?"}
	canonical := MissingSubjectQuestion
	r := ResolveResponse{Status: "needs_clarification", ClarificationKind: "missing_subject", Queries: [][]QueryPart{}, ClarificationQuestion: &canonical}
	if _, err := r.SearchQueries(req); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"", "choice", "missing_subject"} {
		invented := "Это организация, программа или инструмент?"
		r.ClarificationKind, r.ClarificationQuestion = kind, &invented
		if _, err := r.SearchQueries(req); err == nil {
			t.Fatal("freeform alternatives accepted", kind)
		}
	}
	r.ClarificationKind, r.ClarificationQuestion = "missing_subject", &canonical
	req.History = []GenerateMessage{{Role: domain.ChatRoleUser, Content: "Установка уже названа"}}
	if _, err := r.SearchQueries(req); err == nil {
		t.Fatal("existing subject forgotten")
	}
}

func TestResolverAnchorRequiresExplicitIndexAndQuote(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"quote":"item"}`, `{"message_index":null,"quote":"item"}`, `{"message_index":0}`, `{"message_index":0,"quote":null}`, `{"message_index":0,"quote":"item","answer":"invented"}`} {
		var anchor QueryPart
		if json.Unmarshal([]byte(raw), &anchor) == nil {
			t.Fatal("invalid anchor accepted", raw)
		}
	}
}
