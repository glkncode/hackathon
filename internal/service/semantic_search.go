package service

import (
	"context"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SemanticSearchStore interface {
	SearchSemantic(context.Context, uuid.UUID, domain.Role, []float64, domain.EmbeddingIdentity, int) ([]domain.SearchResult, error)
	FilterVisible(context.Context, uuid.UUID, domain.Role, []domain.SearchResult) ([]domain.SearchResult, error)
}
type EvidenceClient interface {
	Evidence(context.Context, string, []domain.SearchResult) ([]uuid.UUID, error)
}
type semanticSearch struct {
	store      SemanticSearchStore
	embed      EmbeddingClient
	evidence   EvidenceClient
	log        *zap.Logger
	supplement bool
	timeout    time.Duration
}

func (s *SearchService) EnableSemantic(store SemanticSearchStore, embed EmbeddingClient, evidence EvidenceClient, log *zap.Logger, supplement bool, timeout time.Duration) {
	s.semantic = &semanticSearch{store, embed, evidence, log, supplement, timeout}
}
func (s *SearchService) searchSemantic(ctx context.Context, actor domain.User, q domain.SearchQuery) ([]domain.SearchResult, error) {
	cfg := s.semantic
	fallback := func(reason string) ([]domain.SearchResult, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		cfg.log.Warn("semantic search FTS fallback", zap.String("reason", reason))
		return s.searchFTS(ctx, actor, q)
	}
	// A semantic branch has one total budget, including the evidence gate;
	// its expiry leaves the caller's original context available for FTS.
	op, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	response, err := cfg.embed.Embed(op, "query", []string{q.Query})
	if err != nil {
		return fallback("embedding_unavailable")
	}
	if response.Validate("query", 1) != nil {
		return fallback("embedding_incompatible")
	}
	pool, err := cfg.store.SearchSemantic(op, actor.ID, actor.Role, response.Embeddings[0], domain.E5Identity(), max(q.Limit, 10))
	if err != nil {
		return fallback("vector_search_unavailable")
	}
	if len(pool) == 0 {
		return fallback("index_not_ready")
	}
	if cfg.supplement {
		fts, err := s.searchFTS(op, actor, domain.SearchQuery{Query: q.Query, Limit: max(q.Limit, 10)})
		if err != nil {
			return fallback("fts_supplement_unavailable")
		}
		seen := map[uuid.UUID]bool{}
		for _, c := range pool {
			seen[c.ChunkID] = true
		}
		for _, c := range fts {
			if !seen[c.ChunkID] && len(pool) < domain.MaxSearchLimit {
				pool = append(pool, c)
				seen[c.ChunkID] = true
			}
		}
	}
	pool, err = cfg.store.FilterVisible(op, actor.ID, actor.Role, pool)
	if err != nil {
		return fallback("visibility_unavailable")
	}
	if len(pool) == 0 {
		return []domain.SearchResult{}, nil
	}
	ids, err := cfg.evidence.Evidence(op, q.Query, pool)
	if err != nil {
		return fallback("evidence_unavailable")
	}
	if op.Err() != nil {
		return fallback("semantic_timeout")
	}
	// An explicit no-evidence verdict abstains. Top-k and cosine are never gates.
	allowed := map[uuid.UUID]bool{}
	for _, id := range ids {
		if allowed[id] {
			return fallback("invalid_evidence_ids")
		}
		allowed[id] = true
	}
	byID := map[uuid.UUID]domain.SearchResult{}
	for _, c := range pool {
		byID[c.ChunkID] = c
	}
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return fallback("invalid_evidence_ids")
		}
	}
	out := make([]domain.SearchResult, 0)
	for _, c := range pool {
		if allowed[c.ChunkID] && len(out) < q.Limit {
			out = append(out, c)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return cfg.store.FilterVisible(ctx, actor.ID, actor.Role, out)
}
