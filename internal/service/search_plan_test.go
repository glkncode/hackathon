package service

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type planStoreStub struct {
	searchStoreStub
	queries []domain.SearchQuery
	lists   [][]domain.SearchResult
	planErr error
	cancel  context.CancelFunc
}

func (s *planStoreStub) SearchVariants(ctx context.Context, id uuid.UUID, role domain.Role, queries []domain.SearchQuery) ([][]domain.SearchResult, error) {
	s.ctx, s.userID, s.role, s.queries = ctx, id, role, queries
	if s.cancel != nil {
		s.cancel()
	}
	return s.lists, s.planErr
}

func TestSearchPlanIdentityBudgetAndExactCitations(t *testing.T) {
	actor := searchActor()
	source := domain.SearchResult{ChunkID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(),
		Content: "Инженер проверяет оборудование.", VersionNumber: 1}
	blank := source
	blank.ChunkID, blank.Content = uuid.New(), "Инженер\t________ [3]"
	store := &planStoreStub{lists: [][]domain.SearchResult{nil, {blank, source}}}
	start := time.Now()
	got, err := NewSearchService(store).Search(context.Background(), actor, domain.SearchQuery{Query: "Какие обязанности у инженера?", Limit: 1})
	if err != nil || len(got) != 1 || store.userID != actor.ID || store.role != actor.Role {
		t.Fatal("wrong actor or plan result", err, got)
	}
	if store.calls != 0 || !reflect.DeepEqual(store.queries, []domain.SearchQuery{
		{Query: "Какие обязанности у инженера?", Limit: 10}, {Query: "инженера", Limit: 10},
	}) {
		t.Fatal("plan bypassed bounded batch search", store.queries)
	}
	deadline, ok := store.ctx.Deadline()
	if !ok || deadline.After(start.Add(5*time.Second+100*time.Millisecond)) {
		t.Fatal("aggregate retrieval budget missing")
	}
	want := source
	want.Score = got[0].Score
	if !reflect.DeepEqual(got[0], want) || got[0].Score <= 0 {
		t.Fatal("source identity/content/metadata fabricated or changed", got)
	}
}

func TestSearchPlanFailureAndCancellationDiscardSources(t *testing.T) {
	source := domain.SearchResult{ChunkID: uuid.New(), Content: "Текст"}
	want := errors.New("storage failed")
	store := &planStoreStub{lists: [][]domain.SearchResult{{source}}, planErr: want}
	got, err := NewSearchService(store).Search(context.Background(), searchActor(), domain.SearchQuery{Query: "Чем занимается инженер?"})
	if !errors.Is(err, want) || got != nil {
		t.Fatal("partial search plan returned on error", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.planErr, store.cancel = nil, cancel
	got, err = NewSearchService(store).Search(ctx, searchActor(), domain.SearchQuery{Query: "Чем занимается инженер?"})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancelled plan returned sources", got, err)
	}
}

func TestSearchFusionDeduplicatesAndRetainsFilledTables(t *testing.T) {
	a := domain.SearchResult{ChunkID: uuid.New(), Content: "Инженер\tПроверяет оборудование", Score: 0.1}
	b := domain.SearchResult{ChunkID: uuid.New(), Content: "Инженер составляет отчёты", Score: 1000}
	c := domain.SearchResult{ChunkID: uuid.New(), Content: "Инженер\t___ [9]"}
	got := fuseSearchResults([][]domain.SearchResult{{b, a, a}, {c, a}}, 1)
	if len(got) != 1 || got[0].ChunkID != a.ChunkID || got[0].Content != a.Content || math.Abs(got[0].Score-(1.0/62+1.0/61)) > 1e-15 {
		t.Fatal("fusion did not use per-list unique ranks / retained blank values", got)
	}
	if got := fuseSearchResults([][]domain.SearchResult{nil, {c}}, 10); got == nil || len(got) != 0 {
		t.Fatal("empty plan should remain empty", got)
	}
}
