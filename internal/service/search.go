package service

import (
	"context"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

// SearchStore must enforce document ACL and active-ready version selection in
// storage, before returning content. Identity comes only from authenticated code.
type SearchStore interface {
	Search(context.Context, uuid.UUID, domain.Role, domain.SearchQuery) ([]domain.SearchResult, error)
}

type SearchService struct{ repository SearchStore }

func NewSearchService(repository SearchStore) *SearchService {
	return &SearchService{repository: repository}
}

// Search can also be called directly by a future ChatService, using a trusted
// authenticated actor. It never calls an LLM and never logs query or source text.
func (s *SearchService) Search(ctx context.Context, actor domain.User, query domain.SearchQuery) ([]domain.SearchResult, error) {
	if actor.ID == uuid.Nil || !actor.IsActive || !actor.Role.Valid() {
		return nil, domain.ErrAccessDenied
	}
	query, err := query.Normalize()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results, err := s.repository.Search(ctx, actor.ID, actor.Role, query)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = make([]domain.SearchResult, 0)
	}
	return results, nil
}
