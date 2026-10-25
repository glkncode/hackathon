package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestConversationalRetrievalBoundaryStates(t *testing.T) {
	for _, mode := range []string{"no_candidates", "unrelated", "fact_absent", "ambiguous", "missing_subject", "gate_error", "embed_error", "invalid_gate_id", "generator_insufficient", "partial_answer"} {
		t.Run(mode, func(t *testing.T) {
			source := ragSource()
			sem := &semanticStub{response: goodEmbedding(), results: []domain.SearchResult{source}}
			search := NewSearchService(&searchStoreStub{})
			switch mode {
			case "no_candidates":
				sem.results = nil
			case "gate_error":
				sem.gateErr = errors.New("offline")
			case "embed_error":
				sem.embedErr = errors.New("offline")
			case "invalid_gate_id":
				sem.ids = []uuid.UUID{uuid.New()}
			case "generator_insufficient", "partial_answer":
				sem.ids = []uuid.UUID{source.ChunkID}
			}
			search.EnableSemantic(sem, sem, sem, zap.NewNop(), false, time.Second)
			store := &contextStore{chatMemory: &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}}
			if mode == "ambiguous" {
				store.history = []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "прибор и комплект"}}
			}
			generated := 0
			ai := contextGenerator{
				resolve: func(_ context.Context, req aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
					if mode == "ambiguous" {
						q := "Вы спрашиваете о «прибор» или о «комплект»?"
						return aiclient.ResolveResponse{Status: "needs_clarification", ClarificationKind: "choice", Queries: [][]aiclient.QueryPart{}, ClarificationQuestion: &q,
							Alternatives: []aiclient.QueryPart{{MessageIndex: 0, Quote: "прибор"}, {MessageIndex: 0, Quote: "комплект"}}}, nil
					}
					if mode == "missing_subject" || mode == "no_candidates" {
						q := aiclient.MissingSubjectQuestion
						return aiclient.ResolveResponse{Status: "needs_clarification", ClarificationKind: "missing_subject", Queries: [][]aiclient.QueryPart{}, ClarificationQuestion: &q}, nil
					}
					return aiclient.ResolveResponse{Status: "independent", Queries: [][]aiclient.QueryPart{{{MessageIndex: -1, Quote: req.Query}}}}, nil
				},
				generate: func(req aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
					generated++
					if mode == "generator_insufficient" {
						return aiclient.GenerateResponse{Status: aiclient.GenerateInsufficientInformation, UsedSourceIDs: []uuid.UUID{}}, nil
					}
					answer := "Выдаёт техник. Стоимость в предоставленных положениях не указана."
					return aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: []uuid.UUID{source.ChunkID}}, nil
				},
			}
			cfg := ragSettings()
			cfg.ContextResolverEnabled = true
			cfg.ContextResolverTimeout = time.Second
			chat, err := NewChatServiceWithRAG(store, search, ai, zap.NewNop(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			actor := searchActor()
			session, _ := chat.CreateSession(context.Background(), actor, "")
			turn, err := chat.SendMessage(context.Background(), actor, session.ID, uuid.New(), "Как получить прибор и сколько это стоит?")
			if mode == "gate_error" || mode == "embed_error" || mode == "invalid_gate_id" {
				if !errors.Is(err, domain.ErrSearchUnavailable) || store.saves != 0 {
					t.Fatal("outage became an assistant answer", err, store.saves)
				}
				return
			}
			if err != nil || turn.AssistantMessage.Mode == nil {
				t.Fatal("missing result", err)
			}
			want := domain.ChatModeNoSources
			if mode == "ambiguous" || mode == "missing_subject" {
				want = domain.ChatModeClarification
			}
			if mode == "partial_answer" {
				want = domain.ChatModeGenerated
			}
			if *turn.AssistantMessage.Mode != want {
				t.Fatalf("mode=%s want=%s", *turn.AssistantMessage.Mode, want)
			}
			if (mode == "generator_insufficient" || mode == "partial_answer") != (generated == 1) {
				t.Fatal("incorrect generation boundary", generated)
			}
			if want != domain.ChatModeGenerated && len(store.lastSources) != 0 {
				t.Fatal("citations attached to refusal or clarification")
			}
		})
	}
}

type invalidRetrievalSearch struct{ contextSearchStub }

func (s *invalidRetrievalSearch) SearchForChat(context.Context, domain.User, domain.SearchQuery, []string) (domain.RetrievalResult, error) {
	return domain.RetrievalResult{}, nil
}

func TestChatRejectsMissingTypedRetrievalResult(t *testing.T) {
	store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
	search := &invalidRetrievalSearch{}
	ai := contextGenerator{resolve: func(_ context.Context, req aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
		return aiclient.ResolveResponse{Status: "independent", Queries: [][]aiclient.QueryPart{{{MessageIndex: -1, Quote: req.Query}}}}, nil
	}}
	cfg := ragSettings()
	cfg.ContextResolverEnabled = true
	cfg.ContextResolverTimeout = time.Second
	chat, err := NewChatServiceWithRAG(store, search, ai, zap.NewNop(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	actor := searchActor()
	session, _ := chat.CreateSession(context.Background(), actor, "")
	_, err = chat.SendMessage(context.Background(), actor, session.ID, uuid.New(), "Понятный вопрос")
	if !errors.Is(err, domain.ErrSearchUnavailable) || store.saves != 0 {
		t.Fatal("missing retrieval became no_sources", err)
	}
}
