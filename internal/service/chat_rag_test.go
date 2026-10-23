package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func ragSource() domain.SearchResult {
	return domain.SearchResult{ChunkID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(), DocumentTitle: "Инструкция", VersionNumber: 1, Content: "секретный фрагмент", Score: 0.5}
}
func ragSettings() RAGConfig {
	return RAGConfig{Timeout: time.Second, MaxSources: 8, MaxHistoryMessages: 10}
}

func TestChatRAGResponsesFallbackAndIdempotency(t *testing.T) {
	for _, tc := range []struct {
		name, status, answer, clarify         string
		invalid, unavailable, empty, disabled bool
		want                                  domain.ChatMode
	}{
		{name: "generated", status: "answered", answer: "Генеративный секретный ответ", want: domain.ChatModeGenerated},
		{name: "clarification", status: "needs_clarification", clarify: "Уточните вид документа", want: domain.ChatModeClarification},
		{name: "insufficient", status: "insufficient_information", want: domain.ChatModeNoSources},
		{name: "unknown ID", status: "answered", answer: "Непроверенный секретный ответ", invalid: true, want: domain.ChatModeSearch},
		{name: "malformed", status: "malformed", want: domain.ChatModeSearch},
		{name: "unavailable", unavailable: true, want: domain.ChatModeSearch},
		{name: "empty retrieval", empty: true, want: domain.ChatModeNoSources},
		{name: "RAG disabled", disabled: true, want: domain.ChatModeSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
			sources := []domain.SearchResult{ragSource(), ragSource()}
			store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
			search := &chatSearchStub{results: sources}
			if tc.empty {
				search.results = nil
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input aiclient.GenerateRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if len(input.Sources) != 2 || input.Sources[0].ChunkID != sources[0].ChunkID {
					t.Error("wrong sources")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.unavailable {
					w.WriteHeader(503)
					return
				}
				if tc.status == "malformed" {
					io.WriteString(w, `{"secret":`)
					return
				}
				result := aiclient.GenerateResponse{Status: aiclient.GenerateStatus(tc.status), UsedSourceIDs: []uuid.UUID{}}
				if tc.answer != "" {
					result.Answer = &tc.answer
					id := sources[0].ChunkID
					if tc.invalid {
						id = uuid.New()
					}
					result.UsedSourceIDs = []uuid.UUID{id}
				}
				if tc.clarify != "" {
					result.ClarificationQuestion = &tc.clarify
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			client, err := aiclient.New(aiclient.Config{URL: server.URL, Token: "private-service-token", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			core, logs := observer.New(zap.DebugLevel)
			log := zap.New(core)
			chat := NewChatService(store, search, log)
			if !tc.disabled {
				chat, err = NewChatServiceWithRAG(store, search, client, log, ragSettings())
				if err != nil {
					t.Fatal(err)
				}
			}
			session, err := chat.CreateSession(ctx, actor, "")
			if err != nil {
				t.Fatal(err)
			}
			key := uuid.New()
			turn, err := chat.SendMessage(ctx, actor, session.ID, key, "секретный вопрос")
			if err != nil || *turn.AssistantMessage.Mode != tc.want || store.saves != 1 {
				t.Fatal(turn, err)
			}
			if (tc.empty || tc.disabled) && calls.Load() != 0 {
				t.Fatal("unnecessary Python call")
			}
			if !tc.empty && !tc.disabled && calls.Load() != 1 {
				t.Fatal("RAG not invoked")
			}
			if tc.want == domain.ChatModeGenerated || tc.want == domain.ChatModeClarification {
				if len(turn.AssistantMessage.DependencyIDs) != 2 {
					t.Fatal("uncited context not tracked")
				}
				if tc.want == domain.ChatModeGenerated && (len(store.lastSources) != 1 || store.lastSources[0].ChunkID != sources[0].ChunkID) {
					t.Fatal("used citations not preserved")
				}
			} else if strings.Contains(turn.AssistantMessage.Content, "секретный") {
				t.Fatal("invalid model answer persisted")
			}
			before := calls.Load()
			replay, err := chat.SendMessage(ctx, actor, session.ID, key, "секретный вопрос")
			if err != nil || replay.AssistantMessage.ID != turn.AssistantMessage.ID || store.saves != 1 || calls.Load() != before {
				t.Fatal("RAG replay not idempotent", err)
			}
			if _, err := chat.SendMessage(ctx, actor, session.ID, key, "different"); !errors.Is(err, domain.ErrChatConflict) {
				t.Fatal(err)
			}
			for _, entry := range logs.All() {
				fields, _ := json.Marshal(entry.ContextMap())
				if strings.Contains(entry.Message+string(fields), "секретный") || strings.Contains(entry.Message+string(fields), "private-service-token") {
					t.Fatal("RAG log leaked text")
				}
			}
		})
	}
}

type ragFailingStore struct {
	*chatMemory
	historyError, saveError error
}

func (s *ragFailingStore) RecentMessages(ctx context.Context, owner, id uuid.UUID, limit int) ([]domain.ChatMessage, error) {
	if s.historyError != nil {
		return nil, s.historyError
	}
	return s.chatMemory.RecentMessages(ctx, owner, id, limit)
}
func (s *ragFailingStore) SaveMessageWithSources(ctx context.Context, owner, id, key uuid.UUID, turn domain.ChatTurn, sources []domain.SearchResult) (domain.ChatTurn, error) {
	if s.saveError != nil {
		return domain.ChatTurn{}, s.saveError
	}
	return s.chatMemory.SaveMessageWithSources(ctx, owner, id, key, turn, sources)
}

type ragGeneratorFunc func(context.Context, aiclient.GenerateRequest) (aiclient.GenerateResponse, error)

func (f ragGeneratorFunc) Generate(ctx context.Context, input aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
	return f(ctx, input)
}

func TestChatRAGDatabaseErrorsAndDeadline(t *testing.T) {
	ctx := context.Background()
	actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	source := ragSource()
	for _, stage := range []string{"history", "save", "timeout", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			store := &ragFailingStore{chatMemory: &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}}
			failure := errors.New("database unavailable")
			if stage == "history" {
				store.historyError = failure
			}
			if stage == "save" {
				store.saveError = failure
			}
			calls := 0
			generator := ragGeneratorFunc(func(ctx context.Context, input aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
				calls++
				if stage == "timeout" || stage == "cancel" {
					<-ctx.Done()
					return aiclient.GenerateResponse{}, ctx.Err()
				}
				answer := "Ответ"
				return aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: []uuid.UUID{source.ChunkID}}, nil
			})
			chat, err := NewChatServiceWithRAG(store, &chatSearchStub{results: []domain.SearchResult{source}}, generator, zap.NewNop(), ragSettings())
			if err != nil {
				t.Fatal(err)
			}
			session, err := chat.CreateSession(ctx, actor, "")
			if err != nil {
				t.Fatal(err)
			}
			requestCtx := ctx
			var cancel context.CancelFunc
			if stage == "cancel" {
				requestCtx, cancel = context.WithCancel(ctx)
				cancel()
			}
			turn, err := chat.SendMessage(requestCtx, actor, session.ID, uuid.New(), "согласование")
			switch stage {
			case "timeout":
				if err != nil || *turn.AssistantMessage.Mode != domain.ChatModeSearch || store.saves != 1 {
					t.Fatal("timeout did not fall back", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || store.saves != 0 {
					t.Fatal("cancelled request saved", err)
				}
			default:
				if !errors.Is(err, failure) || store.saves != 0 {
					t.Fatal("database error hidden", err)
				}
			}
			if stage == "history" && calls != 0 {
				t.Fatal("DB history error reached Python")
			}
		})
	}
}

func TestRAGContextBoundsAndTransitiveProvenance(t *testing.T) {
	source := ragSource()
	oldID := uuid.New()
	mode := domain.ChatModeGenerated
	history := []domain.ChatMessage{
		{Role: domain.ChatRoleAssistant, Mode: &mode, Content: "закрытый ответ", ContentUnavailable: true, DependencyIDs: []uuid.UUID{uuid.New()}},
		{Role: domain.ChatRoleAssistant, Mode: &mode, Content: "доступный ответ", DependencyIDs: []uuid.UUID{oldID}},
		{Role: domain.ChatRoleUser, Content: "продолжение"},
	}
	req, deps := buildGenerateRequest("вопрос", history, []domain.SearchResult{source}, ragSettings())
	if len(req.History) != 2 || req.History[0].Content != "доступный ответ" || len(deps) != 2 || deps[1] != oldID {
		t.Fatal("history redaction or transitive provenance lost")
	}
	large := []domain.SearchResult{source, ragSource(), ragSource()}
	for i := range large {
		large[i].Content = strings.Repeat("x", domain.MaxChunkBytes)
	}
	req, _ = buildGenerateRequest("вопрос", history, large, ragSettings())
	if size, err := req.ContextBytes(); err != nil || size > aiclient.MaxGenerateContextBytes || len(req.Sources) != 1 {
		t.Fatal("context budget", err, size)
	}
	cfg := ragSettings()
	cfg.MaxSources = 1
	cfg.MaxHistoryMessages = 1
	req, _ = buildGenerateRequest("вопрос", history, []domain.SearchResult{source, ragSource()}, cfg)
	if len(req.History) != 1 || len(req.Sources) != 1 || req.History[0].Content != "продолжение" {
		t.Fatal("configured bounds ignored")
	}
}
