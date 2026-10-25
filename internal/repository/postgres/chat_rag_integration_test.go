package postgres

import (
	"errors"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func generatedTurn(mode domain.ChatMode, content string, deps ...uuid.UUID) domain.ChatTurn {
	return domain.ChatTurn{UserMessage: domain.ChatMessage{Role: domain.ChatRoleUser, Content: "согласование"}, AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Mode: &mode, Content: content, DependencyIDs: deps}}
}

func TestIntegrationClarificationWithoutSourcesRemainsReadable(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "Clarification")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	turn, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, key,
		generatedTurn(domain.ChatModeClarification, "Срок какого действия вас интересует?"), nil)
	if err != nil || turn.AssistantMessage.ContentUnavailable || len(turn.AssistantMessage.DependencyIDs) != 0 {
		t.Fatal("source-free clarification could not be saved/read", err)
	}
	history, err := repo.RecentMessages(f.ctx, f.memberA.ID, session.ID, 10)
	if err != nil || len(history) != 2 || history[1].Content != turn.AssistantMessage.Content {
		t.Fatal("clarification lost from next-turn history", err)
	}
}

func TestIntegrationTopicBoundaryPersistsPrivately(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "Topic")
	if err != nil {
		t.Fatal(err)
	}
	turn := chatTestTurn("Independent question", false)
	turn.UserMessage.StartsNewTopic = true
	_, err = repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	// New repository instance models a restart; metadata is read from PostgreSQL.
	history, err := NewChatRepository(f.db).RecentMessages(f.ctx, f.memberA.ID, session.ID, 10)
	if err != nil || len(history) != 2 || !history[0].StartsNewTopic || history[1].StartsNewTopic {
		t.Fatal("topic boundary did not survive restart", err)
	}
	rollbackTo(t, f.ctx, f.db, 11)
	if err = migrator.Up(f.ctx, f.db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	history, err = NewChatRepository(f.db).RecentMessages(f.ctx, f.memberA.ID, session.ID, 10)
	if err != nil || len(history) != 2 || history[0].Content != "Independent question" || history[0].StartsNewTopic {
		t.Fatal("topic migration lost messages or invalid default", err)
	}
}

func TestIntegrationRAGRedactsWholeAnswerForUncitedDependency(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	a, _ := f.document(t, "A", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование порядокA"}})
	b, _ := f.document(t, "B", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование порядокB"}})
	f.grant(t, a.ID, f.memberA.ID)
	f.grant(t, b.ID, f.memberA.ID)
	sources := f.find(t, f.memberA, "согласование", 10)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "RAG")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	secret := strings.Repeat("пересказ закрытого документа ", 30)
	turn, err := repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, key, generatedTurn(domain.ChatModeGenerated, secret, sources[0].ChunkID, sources[1].ChunkID), sources[:1])
	if err != nil || len(turn.AssistantMessage.Sources) != 1 || len(turn.AssistantMessage.DependencyIDs) != 2 || turn.AssistantMessage.Content != secret {
		t.Fatal("generation persistence", err)
	}
	// Revoke the OTHER, uncited document. Model-declared citations are not a
	// sufficient security provenance list, since it saw both input fragments.
	var revokeDoc uuid.UUID
	if sources[1].DocumentID == a.ID {
		revokeDoc = a.ID
	} else {
		revokeDoc = b.ID
	}
	if err := f.acl.RevokeAccess(f.ctx, revokeDoc, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	check := func(messages []domain.ChatMessage) {
		t.Helper()
		for _, m := range messages {
			if m.Role == domain.ChatRoleAssistant && (m.Content != domain.ChatContentUnavailable || !m.ContentUnavailable || len(m.Sources) != 0 || len(m.DependencyIDs) != 0) {
				t.Fatal("closed generated answer/metadata leaked", m.ID)
			}
		}
	}
	history, err := repo.ListMessages(f.ctx, f.memberA.ID, session.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	check(history)
	recent, err := repo.RecentMessages(f.ctx, f.memberA.ID, session.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	check(recent)
	replay, err := repo.GetTurnByClientID(f.ctx, f.memberA.ID, session.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	check([]domain.ChatMessage{replay.AssistantMessage})
	// Concurrent duplicate persistence must use the same redacted replay path.
	replay, err = repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, key, generatedTurn(domain.ChatModeGenerated, secret, sources[0].ChunkID, sources[1].ChunkID), sources[:1])
	if err != nil {
		t.Fatal(err)
	}
	check([]domain.ChatMessage{replay.AssistantMessage})
	f.grant(t, revokeDoc, f.memberA.ID)
	replay, err = NewChatRepository(f.db).GetTurnByClientID(f.ctx, f.memberA.ID, session.ID, key)
	if err != nil || replay.AssistantMessage.Content != secret || replay.AssistantMessage.ContentUnavailable {
		t.Fatal("restored ACL not applied", err)
	}
	// A clarification can also paraphrase a secret even when it cites nothing.
	clarifyKey := uuid.New()
	_, err = repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, clarifyKey, generatedTurn(domain.ChatModeClarification, "Уточнение с закрытыми данными", sources[1].ChunkID), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.acl.RevokeAccess(f.ctx, revokeDoc, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	clarify, err := repo.GetTurnByClientID(f.ctx, f.memberA.ID, session.ID, clarifyKey)
	if err != nil {
		t.Fatal(err)
	}
	check([]domain.ChatMessage{clarify.AssistantMessage})
	// Revocation between generation and saving rolls back user, assistant,
	// citations AND any dependencies already inserted in this transaction.
	var before int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM chat_messages`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), generatedTurn(domain.ChatModeGenerated, "Новый закрытый ответ", sources[0].ChunkID, sources[1].ChunkID), sources[:1])
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatal("revoked dependency was saved", err)
	}
	var after int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM chat_messages`).Scan(&after); err != nil || after != before {
		t.Fatal("partial generated pair", err)
	}
}

func TestIntegrationRAGUsesCurrentRoleAndFailsClosed(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	f.document(t, "Admin doc", domain.VersionReady, []domain.DocumentChunk{{Content: "секретныйпорядок"}})
	sources := f.find(t, f.admin, "секретныйпорядок", 10)
	session, err := repo.CreateSession(f.ctx, f.admin.ID, "Admin")
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	_, err = repo.SaveMessageWithSources(f.ctx, f.admin.ID, session.ID, key, generatedTurn(domain.ChatModeGenerated, "Закрытый текст", sources[0].ChunkID), sources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE users SET role='member' WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := repo.GetTurnByClientID(f.ctx, f.admin.ID, session.ID, key)
	if err != nil || !turn.AssistantMessage.ContentUnavailable || turn.AssistantMessage.Content != domain.ChatContentUnavailable {
		t.Fatal("stale admin role bypassed ACL", err)
	}
	// Defensive reads also hide generated rows whose provenance is absent.
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM chat_message_dependencies WHERE message_id=$1`, turn.AssistantMessage.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE users SET role='admin' WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	turn, err = repo.GetTurnByClientID(f.ctx, f.admin.ID, session.ID, key)
	if err != nil || !turn.AssistantMessage.ContentUnavailable {
		t.Fatal("unprovenanced generated text exposed", err)
	}
	// Rollback is deliberate: old constraints cannot preserve generated modes.
	rollbackTo(t, f.ctx, f.db, 6)
	var mode, content string
	if err := f.db.QueryRowContext(f.ctx, `SELECT mode,content FROM chat_messages WHERE id=$1`, turn.AssistantMessage.ID).Scan(&mode, &content); err != nil || mode != "no_sources" || strings.Contains(content, "Закрытый текст") {
		t.Fatal("unsafe downgrade", err)
	}
	if err := migrator.Up(f.ctx, f.db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRAGDatabaseFailureRollsBackProvenance(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewChatRepository(f.db)
	doc, _ := f.document(t, "Test", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование"}})
	f.grant(t, doc.ID, f.memberA.ID)
	sources := f.find(t, f.memberA, "согласование", 10)
	session, err := repo.CreateSession(f.ctx, f.memberA.ID, "Failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION reject_provenance() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private document content'; END $$;
		CREATE TRIGGER reject_provenance BEFORE INSERT ON chat_message_dependencies FOR EACH ROW EXECUTE FUNCTION reject_provenance()`); err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), generatedTurn(domain.ChatModeGenerated, "Ответ", sources[0].ChunkID), sources)
	if err == nil || strings.Contains(err.Error(), "private document content") {
		t.Fatal("DB failure hidden or leaked", err)
	}
	for _, table := range []string{"chat_messages", "chat_message_sources", "chat_message_dependencies"} {
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed transaction retained rows", table, err)
		}
	}
}
