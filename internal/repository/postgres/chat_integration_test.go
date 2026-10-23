package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func chatTestTurn(content string, hasSources bool) domain.ChatTurn {
	mode, text := domain.SearchReply(hasSources)
	return domain.ChatTurn{UserMessage: domain.ChatMessage{Role: domain.ChatRoleUser, Content: content}, AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: text, Mode: &mode}}
}

func TestIntegrationChatHistoryACLAndPersistence(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	chat := service.NewChatService(repo, service.NewSearchService(f.repo), zap.NewNop())
	doc, v1 := f.document(t, "Регламент", domain.VersionReady, []domain.DocumentChunk{{Content: "Согласование служебных записок", ChunkIndex: 2}})
	f.grant(t, doc.ID, f.memberA.ID)
	session, err := chat.CreateSession(f.ctx, f.memberA, "")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	turn, err := chat.SendMessage(f.ctx, f.memberA, session.ID, key, "согласование")
	if err != nil || len(turn.AssistantMessage.Sources) != 1 || turn.UserMessage.ID == uuid.Nil || turn.AssistantMessage.ID == uuid.Nil || turn.UserMessage.CreatedAt.IsZero() {
		t.Fatal(turn, err)
	}
	source := turn.AssistantMessage.Sources[0]
	if source.DocumentVersionID != v1.ID || source.Score <= 0 || source.Position != 1 {
		t.Fatal(source)
	}
	// Reconstruct repositories/services and rerun migrations, as on an app restart.
	if err := migrator.Up(f.ctx, f.db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	repo = NewChatRepository(f.db)
	chat = service.NewChatService(repo, service.NewSearchService(NewSearchRepository(f.db)), zap.NewNop())
	replay, err := chat.SendMessage(f.ctx, f.memberA, session.ID, key, "согласование")
	if err != nil || replay.UserMessage.ID != turn.UserMessage.ID || replay.AssistantMessage.ID != turn.AssistantMessage.ID || !replay.AssistantMessage.CreatedAt.Equal(turn.AssistantMessage.CreatedAt) {
		t.Fatal("replay after restart", err)
	}
	v2, err := f.docs.CreateVersion(f.ctx, doc.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "v2.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersion(t, v2, domain.VersionReady, []domain.DocumentChunk{{Content: "Новый порядок согласования"}})
	if err := f.docs.SetActiveVersion(f.ctx, doc.ID, v2.ID); err != nil {
		t.Fatal(err)
	}
	history, err := chat.GetSession(f.ctx, f.memberA, session.ID, 20, 0)
	if err != nil || len(history.Messages) != 2 || history.Messages[1].Sources[0].DocumentVersionID != v1.ID || history.Messages[1].Sources[0].ChunkID != source.ChunkID {
		t.Fatal("historical citation changed", err)
	}
	// The FK preserves cited chunk identities and replacement rolls back intact.
	if err := f.chunks.ReplaceVersionChunks(f.ctx, v1.ID, []domain.DocumentChunk{{Content: "replacement"}}); err == nil {
		t.Fatal("cited chunk was deleted")
	}
	old, err := f.chunks.GetChunksByVersion(f.ctx, v1.ID)
	if err != nil || len(old) != 1 || old[0].ID != source.ChunkID || old[0].Content != source.Content {
		t.Fatal("failed reindex corrupted citation", err)
	}
	if err := f.acl.RevokeAccess(f.ctx, doc.ID, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	history, err = chat.GetSession(f.ctx, f.memberA, session.ID, 20, 0)
	if err != nil || len(history.Messages[1].Sources) != 0 {
		t.Fatal("revoked source leaked in history", err)
	}
	replay, err = chat.SendMessage(f.ctx, f.memberA, session.ID, key, "согласование")
	if err != nil || len(replay.AssistantMessage.Sources) != 0 {
		t.Fatal("revoked source leaked in replay", err)
	}
	recent, err := chat.RecentMessages(f.ctx, f.memberA, session.ID, 2)
	if err != nil || len(recent) != 2 || len(recent[1].Sources) != 0 {
		t.Fatal("revoked source leaked in future context", err)
	}
	for _, other := range []domain.User{f.memberB, f.admin} {
		if _, err := repo.GetSessionByID(f.ctx, other.ID, session.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign metadata", err)
		}
		if _, err := repo.ListMessages(f.ctx, other.ID, session.ID, 20, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign history", err)
		}
		if _, err := repo.RecentMessages(f.ctx, other.ID, session.ID, 20); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign context", err)
		}
		if _, err := repo.GetTurnByClientID(f.ctx, other.ID, session.ID, key); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign replay", err)
		}
		if _, err := repo.SaveMessageWithSources(f.ctx, other.ID, session.ID, uuid.New(), chatTestTurn("x", false), nil); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign write", err)
		}
		if err := repo.ArchiveSession(f.ctx, other.ID, session.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("foreign archive", err)
		}
		if items, err := repo.ListSessionsByUser(f.ctx, other.ID, 20, 0); err != nil || len(items) != 0 {
			t.Fatal("foreign session in list", err)
		}
	}
	if err := chat.ArchiveSession(f.ctx, f.memberA, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(f.ctx, f.memberA, session.ID); err != nil {
		t.Fatal(err)
	}
	if items, err := chat.ListSessions(f.ctx, f.memberA, 20, 0); err != nil || len(items) != 0 {
		t.Fatal("archived session listed", err)
	}
	history, err = chat.GetSession(f.ctx, f.memberA, session.ID, 20, 0)
	if err != nil || history.Session.ArchivedAt == nil || len(history.Messages) != 2 {
		t.Fatal("archived history deleted", err)
	}
	if _, err := chat.SendMessage(f.ctx, f.memberA, session.ID, uuid.New(), "new"); !errors.Is(err, domain.ErrChatArchived) {
		t.Fatal(err)
	}
}

func TestIntegrationChatAtomicityAndPagination(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	doc, version := f.document(t, "Test", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование", ChunkIndex: 0}, {Content: "согласование", ChunkIndex: 1}})
	f.grant(t, doc.ID, f.memberA.ID)
	sources := f.find(t, f.memberA, "согласование", 10)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "Test")
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]domain.SearchResult{}, sources...)
	bad[1].ChunkID = uuid.New()
	if _, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), chatTestTurn("согласование", true), bad); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatal("missing source accepted", err)
	}
	for _, table := range []string{"chat_messages", "chat_message_sources"} {
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial turn persisted", table, count, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), chatTestTurn("согласование", true), sources); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 2, 1)
	if err != nil || len(page) != 2 || page[0].Role != domain.ChatRoleAssistant || page[1].Role != domain.ChatRoleUser || len(page[0].Sources) != 2 {
		t.Fatal("pagination/order", err)
	}
	recent, err := repo.RecentMessages(f.ctx, f.memberA.ID, session.ID, 3)
	if err != nil || len(recent) != 3 || recent[0].Role != domain.ChatRoleAssistant || recent[2].Role != domain.ChatRoleAssistant {
		t.Fatal("recent order", err)
	}
	if page, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 2, 20); err != nil || page == nil || len(page) != 0 {
		t.Fatal(page, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := repo.CreateSession(f.ctx, f.memberA.ID, "Other"); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := repo.ListSessionsByUser(f.ctx, f.memberA.ID, 1, 1)
	if err != nil || len(sessions) != 1 {
		t.Fatal(sessions, err)
	}
	if _, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 101, 0); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal(err)
	}
	// A source FK cannot be bypassed even with raw SQL.
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO chat_message_sources(message_id,chunk_id,position,score) VALUES ($1,$2,3,0)`, page[0].ID, uuid.New()); err == nil {
		t.Fatal("missing chunk FK")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE chat_messages SET role='system' WHERE session_id=$1`, session.ID); err == nil {
		t.Fatal("role CHECK missing")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE chat_messages SET mode='llm' WHERE session_id=$1 AND role='assistant'`, session.ID); err == nil {
		t.Fatal("mode CHECK missing")
	}
	// Migration 6 Down removes chat only, preserving indexed originals.
	rollbackTo(t, f.ctx, f.db, 5)
	if chunks, err := f.chunks.GetChunksByVersion(f.ctx, version.ID); err != nil || len(chunks) != 2 {
		t.Fatal("chat Down affected ingestion", err)
	}
	if err := migrator.Up(f.ctx, f.db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationChatConcurrentIdempotency(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "Concurrent")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	const n = 12
	results := make(chan domain.ChatTurn, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, key, chatTestTurn("x", false), nil)
			if err != nil {
				failures <- err
			} else {
				results <- turn
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var id uuid.UUID
	for turn := range results {
		if id == uuid.Nil {
			id = turn.AssistantMessage.ID
		}
		if turn.AssistantMessage.ID != id {
			t.Fatal("duplicate pair created")
		}
	}
	if messages, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 20, 0); err != nil || len(messages) != 2 {
		t.Fatal("duplicate messages", err)
	}
	if _, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, key, chatTestTurn("changed", false), nil); !errors.Is(err, domain.ErrChatConflict) {
		t.Fatal(err)
	}
}

type chatGatedSearch struct {
	entered, release chan struct{}
	search           *service.SearchService
}

func (s *chatGatedSearch) Search(ctx context.Context, actor domain.User, q domain.SearchQuery) ([]domain.SearchResult, error) {
	results, err := s.search.Search(ctx, actor, q)
	close(s.entered)
	select {
	case <-s.release:
		return results, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestIntegrationChatArchiveAndRevokeDuringSearch(t *testing.T) {
	for _, archive := range []bool{true, false} {
		t.Run(map[bool]string{true: "archive", false: "revoke"}[archive], func(t *testing.T) {
			f := newSearchFixture(t)
			repo := NewChatRepository(f.db)
			doc, _ := f.document(t, "Test", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование"}})
			f.grant(t, doc.ID, f.memberA.ID)
			gate := &chatGatedSearch{entered: make(chan struct{}), release: make(chan struct{}), search: service.NewSearchService(f.repo)}
			chat := service.NewChatService(repo, gate, zap.NewNop())
			session, err := chat.CreateSession(f.ctx, f.memberA, "")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := chat.SendMessage(f.ctx, f.memberA, session.ID, uuid.New(), "согласование")
				done <- err
			}()
			select {
			case <-gate.entered:
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
			if archive {
				err = chat.ArchiveSession(f.ctx, f.memberA, session.ID)
			} else {
				err = f.acl.RevokeAccess(f.ctx, doc.ID, f.memberA.ID)
			}
			close(gate.release)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ErrInvalidState
			if archive {
				want = domain.ErrChatArchived
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("stale search was saved", err)
			}
			if messages, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 20, 0); err != nil || len(messages) != 0 {
				t.Fatal("partial/stale turn", err)
			}
		})
	}
}
