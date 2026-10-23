package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type chatMemory struct {
	ChatStore
	session     domain.ChatSession
	turns       map[uuid.UUID]domain.ChatTurn
	saves       int
	lastSources []domain.SearchResult
}

func (m *chatMemory) CreateSession(_ context.Context, owner uuid.UUID, title string) (domain.ChatSession, error) {
	m.session = domain.ChatSession{ID: uuid.New(), UserID: owner, Title: title}
	return m.session, nil
}
func (m *chatMemory) GetSessionByID(_ context.Context, owner, id uuid.UUID) (domain.ChatSession, error) {
	if m.session.UserID != owner || m.session.ID != id {
		return domain.ChatSession{}, domain.ErrNotFound
	}
	return m.session, nil
}
func (m *chatMemory) ListSessionsByUser(_ context.Context, owner uuid.UUID, _, _ int) ([]domain.ChatSession, error) {
	return []domain.ChatSession{m.session}, nil
}
func (m *chatMemory) GetTurnByClientID(_ context.Context, _, _, key uuid.UUID) (domain.ChatTurn, error) {
	if turn, ok := m.turns[key]; ok {
		return turn, nil
	}
	return domain.ChatTurn{}, domain.ErrNotFound
}
func (m *chatMemory) SaveMessageWithSources(_ context.Context, _, _, key uuid.UUID, turn domain.ChatTurn, sources []domain.SearchResult) (domain.ChatTurn, error) {
	m.saves++
	turn.UserMessage.ID = uuid.New()
	turn.AssistantMessage.ID = uuid.New()
	m.turns[key] = turn
	m.lastSources = sources
	return turn, nil
}
func (m *chatMemory) ListMessages(_ context.Context, _, _ uuid.UUID, _, _ int) ([]domain.ChatMessage, error) {
	result := []domain.ChatMessage{}
	for _, turn := range m.turns {
		result = append(result, turn.UserMessage, turn.AssistantMessage)
	}
	return result, nil
}
func (m *chatMemory) RecentMessages(ctx context.Context, owner, id uuid.UUID, limit int) ([]domain.ChatMessage, error) {
	return m.ListMessages(ctx, owner, id, limit, 0)
}
func (m *chatMemory) ArchiveSession(ctx context.Context, owner, id uuid.UUID) error {
	if _, err := m.GetSessionByID(ctx, owner, id); err != nil {
		return err
	}
	now := time.Now()
	m.session.ArchivedAt = &now
	return nil
}

type chatSearchStub struct {
	calls   int
	query   domain.SearchQuery
	actor   domain.User
	results []domain.SearchResult
	err     error
}

func (s *chatSearchStub) Search(_ context.Context, actor domain.User, q domain.SearchQuery) ([]domain.SearchResult, error) {
	s.calls++
	s.actor = actor
	s.query = q
	return s.results, s.err
}

func TestChatLifecycleAndIdempotency(t *testing.T) {
	ctx := context.Background()
	actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
	search := &chatSearchStub{}
	core, logs := observer.New(zap.DebugLevel)
	chat := NewChatService(store, search, zap.New(core))
	session, err := chat.CreateSession(ctx, actor, "")
	if err != nil || session.Title != "Новый диалог" {
		t.Fatal(session, err)
	}
	if items, err := chat.ListSessions(ctx, actor, 20, 0); err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	for _, text := range []string{"", "  ", strings.Repeat("я", 501), "\x00", string([]byte{255})} {
		if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.New(), text); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid content accepted: %v", err)
		}
	}
	if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.Nil, "текст"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal(err)
	}
	if search.calls != 0 || store.saves != 0 {
		t.Fatal("invalid input reached search/storage")
	}
	key := uuid.New()
	turn, err := chat.SendMessage(ctx, actor, session.ID, key, "  секретный вопрос  ")
	if err != nil || *turn.AssistantMessage.Mode != domain.ChatModeNoSources || turn.UserMessage.Content != "секретный вопрос" {
		t.Fatal(turn, err)
	}
	replay, err := chat.SendMessage(ctx, actor, session.ID, key, "секретный вопрос")
	if err != nil || replay.AssistantMessage.ID != turn.AssistantMessage.ID || search.calls != 1 || store.saves != 1 {
		t.Fatal("idempotency", err)
	}
	if _, err := chat.SendMessage(ctx, actor, session.ID, key, "другое сообщение"); !errors.Is(err, domain.ErrChatConflict) {
		t.Fatal(err)
	}
	search.results = []domain.SearchResult{{ChunkID: uuid.New(), Content: "секретный документ", Score: 0.25}}
	turn, err = chat.SendMessage(ctx, actor, session.ID, uuid.New(), "новый вопрос")
	if err != nil || *turn.AssistantMessage.Mode != domain.ChatModeSearch || strings.Contains(turn.AssistantMessage.Content, "секретный документ") || len(store.lastSources) != 1 || search.query.Query != "новый вопрос" || search.actor.ID != actor.ID {
		t.Fatal(turn, err)
	}
	history, err := chat.GetSession(ctx, actor, session.ID, 20, 0)
	if err != nil || len(history.Messages) != 4 {
		t.Fatal(history, err)
	}
	if recent, err := chat.RecentMessages(ctx, actor, session.ID, 20); err != nil || len(recent) != 4 {
		t.Fatal(recent, err)
	}
	search.err = errors.New("секретный вопрос: dependency failed")
	if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.New(), "сбой"); err == nil || store.saves != 2 {
		t.Fatal("search error saved messages")
	}
	if err := chat.ArchiveSession(ctx, actor, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.New(), "вопрос"); !errors.Is(err, domain.ErrChatArchived) {
		t.Fatal(err)
	}
	other := actor
	other.ID = uuid.New()
	other.Role = domain.RoleAdmin
	if _, err := chat.GetSession(ctx, other, session.ID, 20, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("admin read foreign chat", err)
	}
	actor.IsActive = false
	if _, err := chat.CreateSession(ctx, actor, "x"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal(err)
	}
	for _, entry := range logs.All() {
		data, _ := json.Marshal(entry.ContextMap())
		if strings.Contains(entry.Message+string(data), "секретный") {
			t.Fatal("sensitive content logged")
		}
	}
}

func TestChatBounds(t *testing.T) {
	actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	chat := NewChatService(nil, nil, zap.NewNop())
	ctx := context.Background()
	if _, err := chat.CreateSession(ctx, actor, strings.Repeat("я", 201)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, page := range [][2]int{{0, 0}, {101, 0}, {1, -1}, {1, 1_000_001}} {
		if _, err := chat.ListSessions(ctx, actor, page[0], page[1]); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal(err)
		}
		if _, err := chat.GetSession(ctx, actor, uuid.New(), page[0], page[1]); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if _, err := chat.RecentMessages(ctx, actor, uuid.New(), 101); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal(err)
	}
}
