package service

import (
	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"context"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
	"time"
)

type planSearchStub struct {
	semanticStub
	branches            []string
	gateQuery           string
	gatePool            []domain.SearchResult
	searches, gateCalls int
	reject              bool
}

func (s *planSearchStub) Embed(_ context.Context, _ string, queries []string) (aiclient.EmbeddingResponse, error) {
	s.branches = queries
	r := goodEmbedding()
	for i := 1; i < len(queries); i++ {
		v := make([]float64, 384)
		v[i] = 1
		r.Embeddings = append(r.Embeddings, v)
		r.Windows = append(r.Windows, domain.EmbeddingWindow{InputIndex: i, WindowIndex: 0, StartToken: 0, EndToken: 2, TotalTokens: 2})
	}
	return r, nil
}
func (s *planSearchStub) SearchSemantic(_ context.Context, _ uuid.UUID, _ domain.Role, _ []float64, _ domain.EmbeddingIdentity, _ int) ([]domain.SearchResult, error) {
	result := s.results[s.searches%len(s.results)]
	s.searches++
	return []domain.SearchResult{result}, nil
}
func (s *planSearchStub) Evidence(_ context.Context, q string, pool []domain.SearchResult) ([]uuid.UUID, error) {
	s.gateCalls++
	s.gateQuery = q
	s.gatePool = pool
	if s.reject {
		return []uuid.UUID{}, nil
	}
	ids := []uuid.UUID{}
	for _, source := range pool {
		ids = append(ids, source.ChunkID)
	}
	return ids, nil
}

func TestContextSearchMergesComplementarySourcesBeforeSingleGate(t *testing.T) {
	for _, reject := range []bool{false, true} {
		stub := &planSearchStub{semanticStub: semanticStub{results: []domain.SearchResult{ragSource(), ragSource()}}, reject: reject}
		fts := &searchStoreStub{}
		search := NewSearchService(fts)
		search.EnableSemantic(stub, stub, stub, zap.NewNop(), false, time.Second)
		effective := "Предмет А как оформить и передать?"
		out, err := search.SearchContext(context.Background(), searchActor(), domain.SearchQuery{Query: effective}, []string{"А как оформить и передать?", effective, "Предмет передать", effective})
		if err != nil {
			t.Fatal(err)
		}
		if len(stub.branches) != 3 || stub.branches[0] != "А как оформить и передать?" || stub.gateQuery != effective || len(stub.gatePool) != 2 || stub.gateCalls != 1 {
			t.Fatal("query plan/gate contract")
		}
		if reject && (len(out) != 0 || fts.calls != 0) {
			t.Fatal("multi-query bypassed rejection")
		}
		if !reject && len(out) != 2 {
			t.Fatal("complementary sources lost")
		}
	}
}

func TestContextSearchRequiresSemanticGate(t *testing.T) {
	fts := &searchStoreStub{results: []domain.SearchResult{ragSource()}}
	search := NewSearchService(fts)
	_, err := search.SearchContext(context.Background(), searchActor(), domain.SearchQuery{Query: "question"}, []string{"question"})
	if err == nil || fts.calls != 0 {
		t.Fatal("context plan silently skipped semantic gate")
	}
}

func TestContextFusionPreservesExplicitBlankFieldsForGate(t *testing.T) {
	blank := ragSource()
	blank.Content = "Дата назначения\t_____"
	other := ragSource()
	pool := fuseRankedResults([][]domain.SearchResult{{blank}, {blank, other}}, 30, false)
	if len(pool) != 2 || pool[0].ChunkID != blank.ChunkID {
		t.Fatal("blank-field evidence lost before gate")
	}
}
