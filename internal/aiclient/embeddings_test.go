package aiclient

import (
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func embeddingFixture() EmbeddingResponse {
	v := make([]float64, 384)
	v[0] = 1
	return EmbeddingResponse{Model: domain.EmbeddingModel, Revision: domain.EmbeddingRevision, Format: domain.EmbeddingFormat, Dimensions: 384, Embeddings: [][]float64{v}, Windows: []domain.EmbeddingWindow{{EndToken: 10, TotalTokens: 10}}}
}

func TestEvidenceClientValidatesWhitespaceAndSourceAllowlist(t *testing.T) {
	id := uuid.New()
	for _, quote := range []string{"Датчик проверяют каждые шесть месяцев.", "        ", "Датчик проверяют каждые семь месяцев."} {
		t.Run(quote, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/internal/v1/evidence" || r.Header.Get("X-Service-Token") != "private" {
					t.Error("evidence private contract")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"evidence": []map[string]any{{"chunk_id": id, "quote": quote}}})
			}))
			defer server.Close()
			client, err := New(Config{URL: server.URL, Token: "private", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ids, err := client.Evidence(context.Background(), "Когда проверить датчик?", []domain.SearchResult{{ChunkID: id, Content: "Датчик\u00a0проверяют\nкаждые шесть\tмесяцев.", DocumentTitle: "Порядок", VersionNumber: 1}})
			if quote == "Датчик проверяют каждые шесть месяцев." {
				if err != nil || len(ids) != 1 || ids[0] != id {
					t.Fatal("PDF whitespace rejected", err)
				}
			} else if err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
func TestEmbeddingClientValidatesProtocol(t *testing.T) {
	for _, mode := range []string{"ok", "revision", "norm", "nan", "dimension", "missing_window", "truncated", "bad_order", "status", "content_type", "trailing", "unknown_field", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r := embeddingFixture()
			switch mode {
			case "revision":
				r.Revision = "other"
			case "norm":
				r.Embeddings[0][0] = 0
			case "nan":
				r.Embeddings[0][0] = math.NaN()
			case "dimension":
				r.Embeddings[0] = []float64{1}
			case "missing_window":
				r.Windows = nil
			case "truncated":
				r.Windows[0].TotalTokens = 11
			case "bad_order":
				r.Windows[0].InputIndex = 1
			}
			if mode == "nan" {
				if r.Validate("query", 1) == nil {
					t.Fatal("NaN accepted")
				}
				return
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/internal/v1/embeddings" || req.Header.Get("X-Service-Token") != "private" {
					t.Error("private contract")
				}
				var input struct {
					Kind   string   `json:"kind"`
					Inputs []string `json:"inputs"`
				}
				if json.NewDecoder(req.Body).Decode(&input) != nil || input.Kind != "query" || len(input.Inputs) != 1 || input.Inputs[0] != "вопрос" {
					t.Error("input contract")
				}
				if mode == "status" {
					w.WriteHeader(503)
					return
				}
				if mode != "content_type" {
					w.Header().Set("Content-Type", "application/json")
				}
				if mode == "unknown_field" {
					w.Write([]byte(`{"unexpected":true}`))
					return
				}
				json.NewEncoder(w).Encode(r)
				if mode == "trailing" {
					w.Write([]byte(`{}`))
				}
			}))
			defer server.Close()
			client, err := New(Config{URL: server.URL, Token: "private", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			_, err = client.Embed(ctx, "query", []string{"вопрос"})
			if mode == "ok" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("malformed response accepted")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
}
