package service

import (
	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"context"
	"errors"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
	"time"
)

type semanticStub struct {
	response                     aiclient.EmbeddingResponse
	embedErr, searchErr, gateErr error
	results                      []domain.SearchResult
	ids                          []uuid.UUID
	calls                        int
}

func (s *semanticStub) Embed(ctx context.Context, _ string, _ []string) (aiclient.EmbeddingResponse, error) {
	s.calls++
	return s.response, s.embedErr
}
func (s *semanticStub) SearchSemantic(context.Context, uuid.UUID, domain.Role, []float64, domain.EmbeddingIdentity, int) ([]domain.SearchResult, error) {
	return s.results, s.searchErr
}
func (s *semanticStub) Evidence(context.Context, string, []domain.SearchResult) ([]uuid.UUID, error) {
	return s.ids, s.gateErr
}

func (s *semanticStub) FilterVisible(_ context.Context, _ uuid.UUID, _ domain.Role, sources []domain.SearchResult) ([]domain.SearchResult, error) {
	return sources, nil
}
func goodEmbedding() aiclient.EmbeddingResponse {
	v := make([]float64, 384)
	v[0] = 1
	return aiclient.EmbeddingResponse{Model: domain.EmbeddingModel, Revision: domain.EmbeddingRevision, Format: domain.EmbeddingFormat, Dimensions: 384, Embeddings: [][]float64{v}, Windows: []domain.EmbeddingWindow{{InputIndex: 0, WindowIndex: 0, StartToken: 0, EndToken: 2, TotalTokens: 2}}}
}
func TestSemanticGateAndFTSFallback(t *testing.T) {
	id := uuid.New()
	ftsID := uuid.New()
	for _, mode := range []string{"success", "no_evidence", "embed", "invalid", "revision", "sql", "gate", "unknown_id", "partial_index", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			fts := &searchStoreStub{results: []domain.SearchResult{{ChunkID: ftsID}}}
			sem := &semanticStub{response: goodEmbedding(), results: []domain.SearchResult{{ChunkID: id, Score: 0.99}}, ids: []uuid.UUID{id}}
			switch mode {
			case "no_evidence":
				sem.ids = []uuid.UUID{}
			case "embed":
				sem.embedErr = errors.New("offline")
			case "invalid":
				sem.response.Embeddings[0] = nil
			case "revision":
				sem.response.Revision = "other"
			case "sql":
				sem.searchErr = errors.New("db")
			case "gate":
				sem.gateErr = errors.New("qwen")
			case "unknown_id":
				sem.ids = []uuid.UUID{uuid.New()}
			case "partial_index":
				sem.searchErr = domain.ErrNotFound
			}
			svc := NewSearchService(fts)
			svc.EnableSemantic(sem, sem, sem, zap.NewNop(), false, time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			out, err := svc.Search(ctx, searchActor(), domain.SearchQuery{Query: "регламент", Limit: 5})
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || sem.calls != 0 {
					t.Fatal("cancellation ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "success":
				if len(out) != 1 || out[0].ChunkID != id || fts.calls != 0 {
					t.Fatal(out)
				}
			case "no_evidence":
				if len(out) != 0 || fts.calls != 0 {
					t.Fatal("top-k bypassed no-evidence gate")
				}
			default:
				if len(out) != 1 || out[0].ChunkID != ftsID || fts.calls == 0 {
					t.Fatal("FTS unavailable on failure", out)
				}
			}
		})
	}
}
