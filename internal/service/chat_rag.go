package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatGenerator interface {
	Generate(context.Context, aiclient.GenerateRequest) (aiclient.GenerateResponse, error)
}

type RAGConfig struct {
	Timeout                time.Duration
	MaxSources             int
	MaxHistoryMessages     int
	ContextResolverEnabled bool
	ContextResolverTimeout time.Duration
}

func (c RAGConfig) Validate() error {
	if c.Timeout < time.Second || c.Timeout > time.Hour || c.MaxSources < 1 || c.MaxSources > aiclient.MaxGenerateSources || c.MaxHistoryMessages < 0 || c.MaxHistoryMessages > aiclient.MaxGenerateHistory {
		return errors.New("RAG: timeout must be 1s..1h, sources 1..30, history 0..50")
	}
	return nil
}

type chatRAG struct {
	generator ChatGenerator
	config    RAGConfig
}

// The existing constructor keeps search-only callers independent of Python.
func NewChatServiceWithRAG(repository ChatStore, search ChatSearch, generator ChatGenerator, log *zap.Logger, cfg RAGConfig) (*ChatService, error) {
	if cfg.ContextResolverEnabled {
		if cfg.ContextResolverTimeout < time.Second || cfg.ContextResolverTimeout > 30*time.Second {
			return nil, errors.New("resolver timeout must be 1s..30s")
		}
		if _, ok := generator.(ContextResolver); !ok {
			return nil, errors.New("context resolver required")
		}
		if _, ok := search.(ContextSearch); !ok {
			return nil, errors.New("context search required")
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if repository == nil || search == nil || generator == nil || log == nil {
		return nil, errors.New("RAG: chat dependencies are required")
	}
	s := NewChatService(repository, search, log)
	s.rag = &chatRAG{generator: generator, config: cfg}
	return s, nil
}

func buildGenerateRequest(query string, history []domain.ChatMessage, sources []domain.SearchResult, cfg RAGConfig) (aiclient.GenerateRequest, []uuid.UUID) {
	req := aiclient.GenerateRequest{Query: query, History: []aiclient.GenerateMessage{}, Sources: []aiclient.GenerateSource{}}
	dependencies := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	size := len(query)
	for _, source := range sources {
		if len(req.Sources) >= cfg.MaxSources {
			break
		}
		cost := len(source.Content) + len(source.DocumentTitle)
		if source.SectionNumber != nil {
			cost += len(*source.SectionNumber)
		}
		if size+cost > aiclient.MaxGenerateContextBytes {
			continue
		}
		req.Sources = append(req.Sources, aiclient.GenerateSource{ChunkID: source.ChunkID, DocumentTitle: source.DocumentTitle, VersionNumber: source.VersionNumber, SectionNumber: source.SectionNumber, PageNumber: source.PageNumber, Content: source.Content})
		size += cost
		if !seen[source.ChunkID] {
			seen[source.ChunkID] = true
			dependencies = append(dependencies, source.ChunkID)
		}
	}
	// Prefer recent context and drop whole messages, never truncate citations or
	// allow the provenance set to grow without bounds across a long conversation.
	for i := len(history) - 1; i >= 0 && len(req.History) < cfg.MaxHistoryMessages; i-- {
		m := history[i]
		if m.ContentUnavailable || size+len(m.Content) > aiclient.MaxGenerateContextBytes {
			continue
		}
		var extra []uuid.UUID
		if m.Mode != nil && (*m.Mode == domain.ChatModeGenerated || *m.Mode == domain.ChatModeClarification) {
			if *m.Mode == domain.ChatModeGenerated && len(m.DependencyIDs) == 0 {
				continue
			}
			for _, id := range m.DependencyIDs {
				if !seen[id] {
					extra = append(extra, id)
				}
			}
		}
		if len(dependencies)+len(extra) > domain.MaxChatDependencies {
			continue
		}
		for _, id := range extra {
			if !seen[id] {
				seen[id] = true
				dependencies = append(dependencies, id)
			}
		}
		req.History = append(req.History, aiclient.GenerateMessage{Role: m.Role, Content: m.Content})
		size += len(m.Content)
	}
	slices.Reverse(req.History)
	return req, dependencies
}

func (s *ChatService) generateReply(ctx context.Context, session uuid.UUID, query string, history []domain.ChatMessage, sources []domain.SearchResult, fallback domain.ChatMessage) (domain.ChatMessage, []domain.SearchResult) {
	req, dependencies := buildGenerateRequest(query, history, sources, s.rag.config)
	if _, err := req.ContextBytes(); err != nil {
		s.log.Warn("chat RAG fallback", zap.String("session_id", session.String()), zap.String("reason", "context_limit"))
		return fallback, sources
	}
	// Counts expose source loss at the bounded Go->Generator boundary without
	// logging document text or implying that every retrieved source is required.
	s.log.Info("chat generation input", zap.Int("retrieved_source_count", len(sources)), zap.Int("supplied_source_count", len(req.Sources)), zap.Int("omitted_source_count", len(sources)-len(req.Sources)), zap.Int("history_count", len(req.History)))
	ctx, cancel := context.WithTimeout(ctx, s.rag.config.Timeout)
	defer cancel()
	started := time.Now()
	result, err := s.rag.generator.Generate(ctx, req)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = result.Validate(req)
	}
	if err != nil {
		// No remote error/body, URL, input or generated text is logged.
		reason := "invalid_response"
		var remote *aiclient.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = "timeout"
		} else if errors.As(err, &remote) {
			switch remote.Code {
			case "ai_timeout", "ai_unavailable", "ai_rejected":
				reason = remote.Code
			}
			if remote.UpstreamCode != "" {
				reason = aiclient.FailureCode(err)
			}
		}
		s.log.Warn("chat RAG fallback", zap.String("session_id", session.String()), zap.String("reason", reason), zap.Duration("duration", time.Since(started)))
		return fallback, sources
	}
	byID := make(map[uuid.UUID]domain.SearchResult, len(sources))
	for _, source := range sources {
		byID[source.ChunkID] = source
	}
	citations := make([]domain.SearchResult, 0, len(result.UsedSourceIDs))
	for _, id := range result.UsedSourceIDs {
		citations = append(citations, byID[id])
	}
	mode := domain.ChatModeGenerated
	content := ""
	switch result.Status {
	case aiclient.GenerateAnswered:
		content = strings.TrimSpace(*result.Answer)
	case aiclient.GenerateNeedsClarification:
		mode = domain.ChatModeClarification
		content = strings.TrimSpace(*result.ClarificationQuestion)
	case aiclient.GenerateInsufficientInformation:
		mode, content = domain.SearchReply(false)
		dependencies = nil
	}
	s.log.Info("chat generation completed", zap.String("session_id", session.String()), zap.String("mode", string(mode)), zap.String("status", string(result.Status)), zap.Int("source_count", len(citations)), zap.Duration("duration", time.Since(started)))
	return domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: content, Mode: &mode, DependencyIDs: dependencies}, citations
}
