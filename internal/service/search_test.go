package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type searchStoreStub struct {
	calls   int
	userID  uuid.UUID
	role    domain.Role
	query   domain.SearchQuery
	ctx     context.Context
	results []domain.SearchResult
	err     error
}

func (s *searchStoreStub) Search(ctx context.Context, id uuid.UUID, role domain.Role, query domain.SearchQuery) ([]domain.SearchResult, error) {
	s.calls++
	s.userID, s.role, s.query, s.ctx = id, role, query, ctx
	return s.results, s.err
}

func (s *searchStoreStub) SearchVariants(ctx context.Context, id uuid.UUID, role domain.Role, queries []domain.SearchQuery) ([][]domain.SearchResult, error) {
	lists := make([][]domain.SearchResult, 0, len(queries))
	for _, query := range queries {
		results, err := s.Search(ctx, id, role, query)
		if err != nil {
			return nil, err
		}
		lists = append(lists, results)
	}
	return lists, nil
}

func searchActor() domain.User {
	return domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
}

func TestSearchServiceRejectsInvalidQueries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query domain.SearchQuery
	}{
		{"empty", domain.SearchQuery{}},
		{"whitespace", domain.SearchQuery{Query: " \t\r\n\u2003"}},
		{"long", domain.SearchQuery{Query: strings.Repeat("я", domain.MaxSearchRunes+1)}},
		{"negative limit", domain.SearchQuery{Query: "документ", Limit: -1}},
		{"high limit", domain.SearchQuery{Query: "документ", Limit: 31}},
		{"NUL", domain.SearchQuery{Query: "документ\x00"}},
		{"invalid UTF-8", domain.SearchQuery{Query: string([]byte{0xff})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &searchStoreStub{}
			_, err := NewSearchService(store).Search(context.Background(), searchActor(), tc.query)
			if !errors.Is(err, domain.ErrInvalidInput) || store.calls != 0 {
				t.Fatalf("invalid query reached storage: calls=%d error=%v", store.calls, err)
			}
		})
	}
}

func TestSearchServiceRejectsUntrustedActor(t *testing.T) {
	for _, actor := range []domain.User{
		{}, {ID: uuid.New(), Role: domain.RoleMember},
		{ID: uuid.New(), Role: "owner", IsActive: true},
		{Role: domain.RoleAdmin, IsActive: true},
	} {
		store := &searchStoreStub{}
		if _, err := NewSearchService(store).Search(context.Background(), actor, domain.SearchQuery{Query: "документ"}); !errors.Is(err, domain.ErrAccessDenied) || store.calls != 0 {
			t.Fatal("invalid actor reached repository", err)
		}
	}
}

func TestSearchServiceUsesTrustedIdentityAndPreservesSources(t *testing.T) {
	page, section, title := 12, "2.3.1", "Согласование"
	result := domain.SearchResult{ChunkID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(),
		DocumentTitle: "Инструкция", VersionNumber: 2, ChunkIndex: 3, Content: "Точный текст пункта",
		PageNumber: &page, SectionNumber: &section, SectionTitle: &title, Score: 0.123456789}
	for _, role := range []domain.Role{domain.RoleMember, domain.RoleAdmin} {
		actor := searchActor()
		actor.Role = role
		store := &searchStoreStub{results: []domain.SearchResult{result}}
		ctx := context.Background()
		got, err := NewSearchService(store).Search(ctx, actor, domain.SearchQuery{Query: "  согласовать записку \n"})
		if err != nil || !reflect.DeepEqual(got, store.results) || store.userID != actor.ID || store.role != role || store.ctx != ctx {
			t.Fatal("identity, context or citation changed", err)
		}
		if store.query.Query != "согласовать записку" || store.query.Limit != 10 {
			t.Fatal("defaults or normalization missing")
		}
	}
}

func TestSearchServiceEmptyErrorsAndBoundaries(t *testing.T) {
	store := &searchStoreStub{}
	search := NewSearchService(store)
	results, err := search.Search(context.Background(), searchActor(), domain.SearchQuery{Query: strings.Repeat("я", 500), Limit: 30})
	if err != nil || results == nil || len(results) != 0 || store.query.Limit != 30 {
		t.Fatal("empty results or unicode limit rejected", err)
	}
	want := errors.New("repository unavailable")
	store.err = want
	if _, err := search.Search(context.Background(), searchActor(), domain.SearchQuery{Query: "документ"}); !errors.Is(err, want) {
		t.Fatal("repository error swallowed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.calls = 0
	if _, err := search.Search(ctx, searchActor(), domain.SearchQuery{Query: "документ"}); !errors.Is(err, context.Canceled) || store.calls != 0 {
		t.Fatal("cancelled search reached repository", err)
	}
}
