package service

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/queryunderstanding"
	"github.com/google/uuid"
)

// SearchStore must enforce document ACL and active-ready version selection in
// storage, before returning content. Identity comes only from authenticated code.
type SearchStore interface {
	Search(context.Context, uuid.UUID, domain.Role, domain.SearchQuery) ([]domain.SearchResult, error)
	// SearchVariants runs a bounded plan under one visibility/publication snapshot.
	SearchVariants(context.Context, uuid.UUID, domain.Role, []domain.SearchQuery) ([][]domain.SearchResult, error)
}

type SearchService struct{ repository SearchStore }

func NewSearchService(repository SearchStore) *SearchService {
	return &SearchService{repository: repository}
}

// Search is shared by Search HTTP and Chat, using a trusted authenticated actor.
// Query understanding is deterministic and never logs queries or source text.
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
	plan := queryunderstanding.Understand(query.Query)
	if len(plan.Variants) == 1 {
		results, err := s.repository.Search(ctx, actor.ID, actor.Role, query)
		if err != nil {
			return nil, err
		}
		if results == nil {
			results = make([]domain.SearchResult, 0)
		}
		return results, nil
	}
	// Two bounded searches share one total budget and storage snapshot. Each
	// applies the same SQL visibility/relevance checks; no model supplies IDs.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	queries := make([]domain.SearchQuery, 0, len(plan.Variants))
	for _, variant := range plan.Variants {
		// Fetch a small pool before removing empty table cells and applying the
		// caller's top-k; limit=1 must not hide a duty behind a role-only row.
		queries = append(queries, domain.SearchQuery{
			Query: variant, Limit: max(query.Limit, domain.DefaultSearchLimit),
		})
	}
	lists, err := s.repository.SearchVariants(ctx, actor.ID, actor.Role, queries)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fuseSearchResults(lists, query.Limit), nil
}

// A role followed exclusively by blank cells/footnote numbers cannot establish
// duties. Filled table cells are retained. This rule is structural, not a list
// of roles; literal keyword searches keep their original behavior.
func emptyTableValues(content string) bool {
	_, values, table := strings.Cut(content, "\t")
	if !table {
		return false
	}
	return !strings.ContainsFunc(values, unicode.IsLetter)
}

// Reciprocal rank fusion avoids comparing scores from different FTS queries.
// Only repository results may contribute content, IDs or citation metadata.
func fuseSearchResults(lists [][]domain.SearchResult, limit int) []domain.SearchResult {
	const rankConstant = 60.0
	byID := make(map[uuid.UUID]domain.SearchResult)
	for _, list := range lists {
		seen := make(map[uuid.UUID]bool)
		rank := 0
		for _, source := range list {
			if seen[source.ChunkID] || emptyTableValues(source.Content) {
				continue
			}
			seen[source.ChunkID] = true
			rank++
			previous, exists := byID[source.ChunkID]
			if !exists {
				previous = source
				previous.Score = 0
			}
			previous.Score += 1 / (rankConstant + float64(rank))
			byID[source.ChunkID] = previous
		}
	}
	results := make([]domain.SearchResult, 0, len(byID))
	for _, source := range byID {
		results = append(results, source)
	}
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.DocumentID != b.DocumentID {
			return a.DocumentID.String() < b.DocumentID.String()
		}
		if a.DocumentVersionID != b.DocumentVersionID {
			return a.DocumentVersionID.String() < b.DocumentVersionID.String()
		}
		if a.ChunkIndex != b.ChunkIndex {
			return a.ChunkIndex < b.ChunkIndex
		}
		return a.ChunkID.String() < b.ChunkID.String()
	})
	return results[:min(limit, len(results))]
}
