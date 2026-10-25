package postgres

import (
	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"ai-assistant/internal/service"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
)

func TestIntegrationDocumentTurnAtomicRollbackAndRedaction(t *testing.T) {
	f := newSearchFixture(t)
	repo := NewTemplateRepository(f.db)
	chats := NewChatRepository(f.db)
	flows := NewChatDocumentRepository(f.db)
	doc, version, err := f.docs.CreateDocumentWithVersion(f.ctx, domain.Document{Title: "Chat template", CreatedBy: f.admin.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "template.docx", MIMEType: domain.DOCXMIME, SizeBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	f.grant(t, doc.ID, f.memberA.ID)
	template, err := repo.CreateTemplate(f.ctx, domain.DocumentTemplate{DocumentID: doc.ID, DocumentVersionID: version.ID, Name: "Generic", Fields: []domain.TemplateField{{Key: "name", Label: "Имя", Type: "text", Required: true, Question: "Укажите имя", MaxLength: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := chats.CreateSession(f.ctx, f.memberA.ID, "Document")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("simulated lost persistence")
	var draft domain.TemplateDraft
	err = flows.Atomic(f.ctx, f.memberA.ID, session.ID, func(ctx context.Context) error {
		var err error
		draft, err = repo.CreateDraft(ctx, f.memberA.ID, template)
		if err != nil {
			return err
		}
		if err = flows.Save(ctx, domain.ChatDocumentWorkflow{SessionID: session.ID, OwnerID: f.memberA.ID, DraftID: &draft.ID, Stage: "collecting_fields"}); err != nil {
			return err
		}
		mode := domain.ChatModeDocument
		_, err = chats.SaveMessageWithSources(ctx, f.memberA.ID, session.ID, uuid.New(), domain.ChatTurn{UserMessage: domain.ChatMessage{Role: domain.ChatRoleUser, Content: "private answer", DocumentDraftID: &draft.ID}, AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: "saved", Mode: &mode, DocumentDraftID: &draft.ID}}, nil)
		if err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err = repo.GetDraft(f.ctx, f.memberA.ID, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("draft escaped rollback", err)
	}
	if _, err = flows.Get(f.ctx, f.memberA.ID, session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("workflow escaped rollback")
	}
	messages, err := chats.ListMessages(f.ctx, f.memberA.ID, session.ID, 100, 0)
	if err != nil || len(messages) != 0 {
		t.Fatal("messages escaped rollback")
	}
	// Real TemplateService and repository orchestrate arbitrary schema, no profile-specific questions.
	access := service.NewDocumentAccessService(f.acl, f.docs, NewUserRepository(f.db), zap.NewNop())
	templates := service.NewTemplateService(repo, f.docs, access, nil, nil, zap.NewNop())
	chat := service.NewChatService(chats, service.NewSearchService(f.repo), zap.NewNop())
	chat.EnableDocuments(flows, templates, nil)
	a := service.ChatDocumentAction{Action: "choose", TemplateID: template.ID, ClientMessageID: uuid.New()}
	turn, err := chat.SendDocumentAction(f.ctx, f.memberA, session.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	view, err := chat.DocumentView(f.ctx, f.memberA, session.ID)
	if err != nil || view.Draft == nil {
		t.Fatal(err)
	}
	if replay, err := chat.SendDocumentAction(f.ctx, f.memberA, session.ID, a); err != nil || replay.AssistantMessage.ID != turn.AssistantMessage.ID {
		t.Fatal("replay created another draft", err)
	}
	d := *view.Draft
	a = service.ChatDocumentAction{Action: "answer", ClientMessageID: uuid.New(), ExpectedRevision: d.Revision, WorkflowVersion: view.Workflow.Version, Values: map[string]json.RawMessage{"name": json.RawMessage(`"Иванов"`)}}
	if _, err := chat.SendDocumentAction(f.ctx, f.memberA, session.ID, a); err != nil {
		t.Fatal(err)
	}
	a.ClientMessageID = uuid.New()
	if _, err := chat.SendDocumentAction(f.ctx, f.memberA, session.ID, a); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatal("stale tab accepted", err)
	}
	if _, err = chat.DocumentView(f.ctx, f.memberB, session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign conversation visible")
	}
	if err = f.acl.RevokeAccess(f.ctx, doc.ID, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	view, err = chat.DocumentView(f.ctx, f.memberA, session.ID)
	if err != nil || !view.Unavailable || view.Draft != nil {
		t.Fatal("revoked draft exposed")
	}
	messages, err = chats.ListMessages(f.ctx, f.memberA.ID, session.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.Content != domain.ChatContentUnavailable {
			t.Fatal("revoked field reply exposed")
		}
	}
	recent, err := chats.RecentMessages(f.ctx, f.memberA.ID, session.ID, 100)
	if err != nil || len(recent) != 0 {
		t.Fatal("draft fields entered RAG history", err)
	}
	mode, ordinaryReply := domain.SearchReply(false)
	_, err = chats.SaveMessageWithSources(f.ctx, f.memberA.ID, session.ID, uuid.New(), domain.ChatTurn{
		UserMessage:      domain.ChatMessage{Role: domain.ChatRoleUser, Content: "ordinary question"},
		AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: ordinaryReply, Mode: &mode},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Down to 12 preserves the actual draft and every old conversation message.
	rollbackTo(t, f.ctx, f.db, 12)
	var count int
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM chat_messages WHERE session_id=$1`, session.ID).Scan(&count); err != nil || count != 6 {
		t.Fatal("migration deleted conversation", err)
	}
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM chat_messages WHERE session_id=$1 AND content=$2`, session.ID, domain.ChatContentUnavailable).Scan(&count); err != nil || count != 4 {
		t.Fatal("rollback exposed document text without ACL links", err)
	}
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM chat_messages WHERE session_id=$1 AND content IN ($2,$3)`, session.ID, "ordinary question", ordinaryReply).Scan(&count); err != nil || count != 2 {
		t.Fatal("rollback changed ordinary conversation", err)
	}
	if _, err = repo.GetDraft(f.ctx, f.memberA.ID, d.ID); err != nil {
		t.Fatal("migration deleted draft")
	}
	if err = migrator.Up(f.ctx, f.db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
}
