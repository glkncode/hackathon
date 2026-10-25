package service

import (
	"ai-assistant/internal/aiclient"
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

// SearchContext keeps the original public contract for existing callers.
func (s *SearchService) SearchContext(ctx context.Context, actor domain.User, q domain.SearchQuery, branches []string) ([]domain.SearchResult, error) {
	if len(branches) == 0 {
		return nil, domain.ErrInvalidInput
	}
	result, err := s.SearchForChat(ctx, actor, q, branches)
	return result.Sources, err
}

func (s *SearchService) searchSemanticPlan(ctx context.Context, actor domain.User, q domain.SearchQuery, branches []string) (domain.RetrievalResult, error) {
	cfg := s.semantic
	fallback := func(reason string) (domain.RetrievalResult, error) {
		if ctx.Err() != nil {
			return domain.RetrievalResult{}, ctx.Err()
		}
		cfg.log.Warn("semantic search FTS fallback", zap.String("reason", reason))
		results, err := s.searchFTS(ctx, actor, q)
		if err == nil && len(results) == 0 && reason != "index_not_ready" {
			// An empty degraded search cannot prove that evidence is absent.
			return domain.RetrievalResult{}, domain.ErrSearchUnavailable
		}
		return retrievalResult(results, domain.RetrievalNoCandidates), err
	}
	// A semantic branch has one total budget, including the evidence gate;
	// its expiry leaves the caller's original context available for FTS.
	op, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	response, err := cfg.embed.Embed(op, "query", branches)
	if err != nil {
		return fallback("embedding_unavailable")
	}
	if response.Validate("query", len(branches)) != nil {
		return fallback("embedding_incompatible")
	}
	lists := [][]domain.SearchResult{}
	for i := range branches {
		candidates, err := cfg.store.SearchSemantic(op, actor.ID, actor.Role, response.Embeddings[i], domain.E5Identity(), max(q.Limit, 10))
		if err != nil {
			return fallback("vector_search_unavailable")
		}
		lists = append(lists, candidates)
	}
	var pool []domain.SearchResult
	if len(lists) == 1 {
		pool = lists[0]
	} else {
		// Semantic candidates include explicit blank fields as possible evidence.
		// Do not apply the FTS-only empty-table duty filter before Evidence Gate.
		pool = fuseRankedResults(lists, domain.MaxSearchLimit, false)
	}
	cfg.log.Info("retrieval candidates", zap.Int("branch_count", len(branches)), zap.Int("candidate_count", len(pool)))
	if len(pool) == 0 {
		cfg.log.Info("retrieval verdict", zap.String("reason", "no_candidates"), zap.Int("candidate_count", 0))
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
	beforeVisibility := len(pool)
	pool, err = cfg.store.FilterVisible(op, actor.ID, actor.Role, pool)
	if err != nil {
		return fallback("visibility_unavailable")
	}
	cfg.log.Info("retrieval visibility", zap.Int("candidate_count", beforeVisibility), zap.Int("candidate_count_after_acl", len(pool)))
	if len(pool) == 0 {
		cfg.log.Info("retrieval verdict", zap.String("reason", "no_visible_candidates"), zap.Int("candidate_count", 0))
		return retrievalResult(nil, domain.RetrievalNoCandidates), nil
	}
	gateStarted := time.Now()
	ids, err := cfg.evidence.Evidence(op, q.Query, pool)
	cfg.log.Info("evidence gate completed", zap.Int("candidate_count", len(pool)), zap.Int("evidence_count", len(ids)), zap.Bool("success", err == nil), zap.String("error_code", aiclient.FailureCode(err)), zap.Duration("duration", time.Since(gateStarted)))
	if err != nil {
		return fallback("evidence_unavailable")
	}
	if op.Err() != nil {
		return fallback("semantic_timeout")
	}
	if len(ids) == 0 {
		cfg.log.Info("retrieval verdict", zap.String("reason", "evidence_rejected"), zap.Int("candidate_count", len(pool)))
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
		return domain.RetrievalResult{}, ctx.Err()
	}
	visible, err := cfg.store.FilterVisible(ctx, actor.ID, actor.Role, out)
	if len(ids) == 0 {
		return retrievalResult(visible, domain.RetrievalRejected), err
	}
	return retrievalResult(visible, domain.RetrievalNoCandidates), err
}
