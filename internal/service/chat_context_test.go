package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type contextStore struct {
	*chatMemory
	history []domain.ChatMessage
}

func (s *contextStore) RecentMessages(ctx context.Context, owner, id uuid.UUID, limit int) ([]domain.ChatMessage, error) {
	if _, err := s.GetSessionByID(ctx, owner, id); err != nil {
		return nil, err
	}
	return s.history, nil
}

type contextSearchStub struct {
	chatSearchStub
	branches  []string
	effective string
}

func (s *contextSearchStub) SearchContext(_ context.Context, _ domain.User, q domain.SearchQuery, branches []string) ([]domain.SearchResult, error) {
	s.branches = branches
	s.effective = q.Query
	return s.results, nil
}

func (s *contextSearchStub) SearchForChat(ctx context.Context, actor domain.User, q domain.SearchQuery, branches []string) (domain.RetrievalResult, error) {
	var sources []domain.SearchResult
	var err error
	if branches == nil {
		sources, err = s.Search(ctx, actor, q)
	} else {
		sources, err = s.SearchContext(ctx, actor, q, branches)
	}
	return retrievalResult(sources, domain.RetrievalRejected), err
}

type contextGenerator struct {
	resolve  func(context.Context, aiclient.ResolveRequest) (aiclient.ResolveResponse, error)
	generate func(aiclient.GenerateRequest) (aiclient.GenerateResponse, error)
}

func (g contextGenerator) Resolve(ctx context.Context, r aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
	return g.resolve(ctx, r)
}
func (g contextGenerator) Generate(_ context.Context, r aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
	return g.generate(r)
}

func TestChatContextResolutionFallbackIsolationAndProvenance(t *testing.T) {
	for _, mode := range []string{"resolved", "independent", "clarify", "clarify_with_evidence", "error", "invalid", "timeout", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			source := ragSource()
			oldID := uuid.New()
			generated := domain.ChatModeGenerated
			store := &contextStore{chatMemory: &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}, history: []domain.ChatMessage{
				{Role: domain.ChatRoleUser, Content: "Как получить прибор или запасной комплект?", StartsNewTopic: true},
				{Role: domain.ChatRoleAssistant, Content: "Прибор выдаёт техник.", Mode: &generated, DependencyIDs: []uuid.UUID{oldID}},
				{Role: domain.ChatRoleAssistant, Content: "SECRET REVOKED", Mode: &generated, ContentUnavailable: true, DependencyIDs: []uuid.UUID{uuid.New()}},
			}}
			if mode == "clarify_with_evidence" {
				store.history = append([]domain.ChatMessage{
					{Role: domain.ChatRoleUser, Content: "Old unrelated topic", StartsNewTopic: true},
					{Role: domain.ChatRoleAssistant, Content: "Old unrelated answer", Mode: &generated, DependencyIDs: []uuid.UUID{uuid.New()}},
				}, store.history...)
			}
			search := &contextSearchStub{chatSearchStub: chatSearchStub{results: []domain.SearchResult{source}}}
			if mode == "clarify" {
				search.results = nil
			}
			actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
			resolveCalls, generateCalls := 0, 0
			question := "А когда его вернуть?"
			generator := contextGenerator{
				resolve: func(ctx context.Context, req aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
					resolveCalls++
					if len(req.History) != 2 || strings.Contains(req.History[1].Content, "SECRET") {
						t.Fatal("revoked history leaked")
					}
					if mode == "error" {
						return aiclient.ResolveResponse{}, errors.New("offline")
					}
					if mode == "timeout" {
						<-ctx.Done()
						return aiclient.ResolveResponse{}, ctx.Err()
					}
					if mode == "clarify" || mode == "clarify_with_evidence" {
						q := "Вы спрашиваете о «прибор» или о «запасной комплект»?"
						return aiclient.ResolveResponse{Status: "needs_clarification", ClarificationKind: "choice", Queries: [][]aiclient.QueryPart{}, ClarificationQuestion: &q, LLMCalls: 1, Alternatives: []aiclient.QueryPart{{MessageIndex: 0, Quote: "прибор"}, {MessageIndex: 0, Quote: "запасной комплект"}}}, nil
					}
					result := aiclient.ResolveResponse{Status: "resolved", Subject: &aiclient.QueryPart{MessageIndex: 0, Quote: "прибор"}, Queries: [][]aiclient.QueryPart{{{MessageIndex: 0, Quote: "прибор"}, {MessageIndex: -1, Quote: question}}}}
					if mode == "independent" {
						result.Status = "independent"
						result.Subject = nil
						result.Queries[0] = result.Queries[0][1:]
					}
					if mode == "invalid" {
						result.Queries[0][0].Quote = "выдуманный предмет"
					}
					return result, nil
				},
				generate: func(req aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
					generateCalls++
					if mode == "resolved" && (req.Query != question || len(req.History) != 2) {
						t.Fatal("resolved generation context")
					}
					if mode == "clarify_with_evidence" && (len(req.History) != 2 || strings.Contains(req.History[0].Content, "Old")) {
						t.Fatal("old topic re-entered after provisional clarification")
					}
					if mode == "independent" && len(req.History) != 0 {
						t.Fatal("old topic sent to generation")
					}
					answer := "Верните прибор технику."
					return aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: []uuid.UUID{source.ChunkID}}, nil
				},
			}
			cfg := ragSettings()
			cfg.ContextResolverEnabled = mode != "disabled"
			cfg.ContextResolverTimeout = time.Second
			chat, err := NewChatServiceWithRAG(store, search, generator, zap.NewNop(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			session, _ := chat.CreateSession(context.Background(), actor, "")
			key := uuid.New()
			turn, err := chat.SendMessage(context.Background(), actor, session.ID, key, question)
			if err != nil {
				t.Fatal(err)
			}
			if turn.UserMessage.Content != question {
				t.Fatal("original question overwritten")
			}
			if mode == "disabled" {
				if resolveCalls != 0 || chat.ContextMetrics().Calls != 0 {
					t.Fatal("flag ignored")
				}
			} else if resolveCalls != 1 {
				t.Fatal("resolver call count")
			}
			if mode == "clarify" {
				if generateCalls != 0 || search.calls != 1 || len(search.branches) != 0 || *turn.AssistantMessage.Mode != domain.ChatModeClarification {
					t.Fatal("clarification searched or generated")
				}
			} else if mode == "resolved" || mode == "independent" {
				if len(search.branches) != 2 || search.branches[0] != question || search.calls != 0 {
					t.Fatal("original branch lost")
				}
			} else if search.calls != 1 || search.query.Query != question {
				t.Fatal("old search fallback lost")
			}
			if mode == "resolved" || mode == "clarify" || mode == "clarify_with_evidence" {
				found := false
				for _, dep := range turn.AssistantMessage.DependencyIDs {
					found = found || dep == oldID
				}
				if !found {
					t.Fatal("resolver history provenance lost")
				}
			}
			metrics := chat.ContextMetrics()
			if (mode == "clarify" || mode == "clarify_with_evidence") && metrics.Calls != 1 {
				t.Fatal("resolver call not counted", metrics)
			}
			if mode == "timeout" && (metrics.Timeouts != 1 || metrics.Failures != 1) {
				t.Fatal(metrics)
			}
			_, err = chat.SendMessage(context.Background(), actor, session.ID, key, question)
			if err != nil || metrics != chat.ContextMetrics() {
				t.Fatal("retry calls resolver")
			}
			other := actor
			other.ID = uuid.New()
			if _, err = chat.SendMessage(context.Background(), other, session.ID, uuid.New(), question); err == nil {
				t.Fatal("session ownership bypassed")
			}
		})
	}
}

func TestResolverHistoryBounds(t *testing.T) {
	history := []domain.ChatMessage{}
	for range 60 {
		history = append(history, domain.ChatMessage{Role: domain.ChatRoleUser, Content: strings.Repeat("я", 500)})
	}
	messages, _ := resolverHistory("вопрос", history)
	size := len("вопрос")
	for _, m := range messages {
		size += len(m.Content)
	}
	if len(messages) > 16 || size > 16<<10 {
		t.Fatal("history budget exceeded")
	}
}

func TestActiveTopicBoundaryExcludesOldSubject(t *testing.T) {
	history := []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: "old subject", StartsNewTopic: true},
		{Role: domain.ChatRoleAssistant, Content: "old answer"},
		{Role: domain.ChatRoleUser, Content: "new subject", StartsNewTopic: true},
		{Role: domain.ChatRoleUser, Content: "follow-up"},
	}
	active := activeTopicHistory(history)
	if len(active) != 2 || active[0].Content != "new subject" {
		t.Fatal("old topic included")
	}
	messages, _ := resolverHistory("next", history)
	if len(messages) != 2 || messages[0].Content != "new subject" {
		t.Fatal("resolver topic boundary ignored")
	}
}
