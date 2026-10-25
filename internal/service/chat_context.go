package service

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ContextResolver interface {
	Resolve(context.Context, aiclient.ResolveRequest) (aiclient.ResolveResponse, error)
}
type ContextSearch interface {
	SearchContext(context.Context, domain.User, domain.SearchQuery, []string) ([]domain.SearchResult, error)
	SearchForChat(context.Context, domain.User, domain.SearchQuery, []string) (domain.RetrievalResult, error)
}

type resolverMetrics struct{ calls, failures, timeouts, nanoseconds atomic.Int64 }
type ResolverMetrics struct{ Calls, Failures, Timeouts, DurationMS int64 }

func (s *ChatService) ContextMetrics() ResolverMetrics {
	m := &s.contextMetrics
	return ResolverMetrics{m.calls.Load(), m.failures.Load(), m.timeouts.Load(), m.nanoseconds.Load() / int64(time.Millisecond)}
}

// A persisted, validated independent question ends older topics even after a
// restart. Older sessions without markers retain the bounded legacy history.
func activeTopicHistory(history []domain.ChatMessage) []domain.ChatMessage {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == domain.ChatRoleUser && history[i].StartsNewTopic {
			return history[i:]
		}
	}
	return history
}

// Whole, recent messages only; the repository has already checked session
// ownership and redacted revoked document dependencies. No cross-session cache.
func resolverHistory(query string, history []domain.ChatMessage) ([]aiclient.GenerateMessage, []uuid.UUID) {
	history = activeTopicHistory(history)
	messages := []aiclient.GenerateMessage{}
	dependencies := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	size := len(query)
	for i := len(history) - 1; i >= 0 && len(messages) < 16; i-- {
		m := history[i]
		if m.ContentUnavailable || utf8.RuneCountInString(m.Content) > 4000 || size+len(m.Content) > 16<<10 {
			continue
		}
		if m.Role == domain.ChatRoleAssistant && (m.Mode == nil || (*m.Mode != domain.ChatModeGenerated && *m.Mode != domain.ChatModeClarification)) {
			continue
		}
		if m.Mode != nil && *m.Mode == domain.ChatModeGenerated && len(m.DependencyIDs) == 0 {
			continue
		}
		extra := []uuid.UUID{}
		for _, id := range m.DependencyIDs {
			if !seen[id] {
				extra = append(extra, id)
			}
		}
		// Reserve space for all current generation sources.
		if len(dependencies)+len(extra) > domain.MaxChatDependencies-aiclient.MaxGenerateSources {
			continue
		}
		for _, id := range extra {
			seen[id] = true
			dependencies = append(dependencies, id)
		}
		messages = append(messages, aiclient.GenerateMessage{Role: m.Role, Content: m.Content})
		size += len(m.Content)
	}
	slices.Reverse(messages)
	return messages, dependencies
}

func (s *ChatService) resolveContext(ctx context.Context, session uuid.UUID, query string, history []domain.ChatMessage) (aiclient.ResolveResponse, []string, []uuid.UUID, error) {
	messages, deps := resolverHistory(query, history)
	req := aiclient.ResolveRequest{Query: query, History: messages, TimeoutMS: int(s.rag.config.ContextResolverTimeout / time.Millisecond)}
	op, cancel := context.WithTimeout(ctx, s.rag.config.ContextResolverTimeout)
	defer cancel()
	started := time.Now()
	s.contextMetrics.calls.Add(1)
	result, err := s.rag.generator.(ContextResolver).Resolve(op, req)
	llmCalls := max(1, min(2, result.LLMCalls))
	if llmCalls > 1 {
		s.contextMetrics.calls.Add(int64(llmCalls - 1))
	}
	if err == nil {
		err = op.Err()
	}
	var queries []string
	if err == nil {
		queries, err = result.SearchQueries(req)
	}
	outcome := result.Status
	reason := ""
	if err != nil {
		reason = aiclient.FailureCode(err)
		outcome = "fallback"
		s.contextMetrics.failures.Add(1)
		if errors.Is(op.Err(), context.DeadlineExceeded) || reason == "resolver_timeout" || reason == "llm_timeout" {
			if errors.Is(op.Err(), context.DeadlineExceeded) {
				reason = "resolver_timeout"
			}
			outcome = "timeout"
			s.contextMetrics.timeouts.Add(1)
		}
	}
	duration := time.Since(started)
	s.contextMetrics.nanoseconds.Add(int64(duration))
	metrics := s.ContextMetrics()
	s.log.Info("chat context resolver completed", zap.String("session_id", session.String()), zap.String("outcome", outcome), zap.String("reason", reason), zap.Duration("duration", duration), zap.Int("history_count", len(messages)), zap.Int("query_count", len(queries)),
		zap.Int("llm_calls", llmCalls),
		zap.Int64("calls_total", metrics.Calls), zap.Int64("failures_total", metrics.Failures), zap.Int64("timeouts_total", metrics.Timeouts), zap.Int64("duration_ms_total", metrics.DurationMS))
	if result.Status == "independent" {
		deps = nil
	}
	return result, queries, deps, err
}
