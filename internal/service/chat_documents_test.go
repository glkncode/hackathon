package service

import (
	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
)

type workflowMemory struct{ flow *domain.ChatDocumentWorkflow }

func (w *workflowMemory) Get(context.Context, uuid.UUID, uuid.UUID) (domain.ChatDocumentWorkflow, error) {
	if w.flow == nil {
		return domain.ChatDocumentWorkflow{}, domain.ErrNotFound
	}
	return *w.flow, nil
}
func (w *workflowMemory) Save(_ context.Context, f domain.ChatDocumentWorkflow) error {
	f.Version++
	w.flow = &f
	return nil
}
func (w *workflowMemory) Atomic(ctx context.Context, _, _ uuid.UUID, fn func(context.Context) error) error {
	return fn(ctx)
}

type documentRepoMemory struct {
	*templateRepoFake
	creates int
	catalog []domain.DocumentTemplate
}

func (r *documentRepoMemory) ListTemplates(context.Context, uuid.UUID, int, int) ([]domain.DocumentTemplate, error) {
	if r.catalog != nil {
		return r.catalog, nil
	}
	return []domain.DocumentTemplate{r.template}, nil
}
func (r *documentRepoMemory) GetTemplate(_ context.Context, id uuid.UUID) (domain.DocumentTemplate, error) {
	for _, candidate := range append([]domain.DocumentTemplate{r.template}, r.catalog...) {
		if candidate.ID == id {
			return candidate, nil
		}
	}
	return domain.DocumentTemplate{}, domain.ErrNotFound
}
func (r *documentRepoMemory) CreateDraft(_ context.Context, owner uuid.UUID, t domain.DocumentTemplate) (domain.TemplateDraft, error) {
	r.creates++
	r.draft = domain.TemplateDraft{ID: uuid.New(), TemplateID: t.ID, DocumentVersionID: t.DocumentVersionID, OwnerID: owner, Revision: 1, Fields: t.Fields, Values: map[string]json.RawMessage{}}
	return r.draft, nil
}

type interpreterStub struct {
	plan  aiclient.DocumentInterpretation
	err   error
	calls int
	last  aiclient.DocumentInterpretRequest
}

func (m *interpreterStub) InterpretDocument(_ context.Context, r aiclient.DocumentInterpretRequest) (aiclient.DocumentInterpretation, error) {
	m.calls++
	m.last = r
	return m.plan, m.err
}

func TestDocumentIntentsPreserveRAGAndValidateModel(t *testing.T) {
	for _, intent := range []string{"knowledge_question", "create_document", "ambiguous", "template_catalog", "invented_template", "model_error"} {
		t.Run(intent, func(t *testing.T) {
			actor := domain.User{ID: uuid.New(), IsActive: true, Role: domain.RoleMember}
			store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
			search := &chatSearchStub{}
			chat := NewChatService(store, search, zap.NewNop())
			session, _ := chat.CreateSession(context.Background(), actor, "")
			repo := &documentRepoMemory{templateRepoFake: &templateRepoFake{template: domain.DocumentTemplate{ID: uuid.New(), Enabled: true, Fields: []domain.TemplateField{{Key: "request", Label: "Запрос", Question: "Что требуется?", Type: "text", Required: true, MaxLength: 100}}}}}
			templates := NewTemplateService(repo, nil, &templateAccessFake{}, nil, nil, zap.NewNop())
			flow := &workflowMemory{}
			model := &interpreterStub{plan: aiclient.DocumentInterpretation{Intent: intent, TemplateIDs: []string{}, Changes: map[string]json.RawMessage{}}}
			if intent == "create_document" {
				model.plan.TemplateIDs = []string{repo.template.ID.String()}
			}
			if intent == "invented_template" {
				model.plan.Intent = "create_document"
				model.plan.TemplateIDs = []string{uuid.NewString()}
			}
			if intent == "model_error" {
				model.err = errors.New("timeout")
			}
			chat.EnableDocuments(flow, templates, model)
			key := uuid.New()
			turn, err := chat.SendMessage(context.Background(), actor, session.ID, key, "Свободная формулировка пользователя")
			if err != nil {
				t.Fatal(err)
			}
			if intent == "create_document" {
				if repo.creates != 1 || flow.flow.DraftID == nil || search.calls != 0 {
					t.Fatal("creation did not reuse templates")
				}
			}
			if intent != "create_document" && repo.creates != 0 {
				t.Fatal("noncreation intent created a draft")
			}
			if intent == "knowledge_question" && search.calls != 1 {
				t.Fatal("RAG bypassed")
			}
			if _, err = chat.SendMessage(context.Background(), actor, session.ID, key, "Свободная формулировка пользователя"); err != nil || model.calls != 1 {
				t.Fatal("replay called model", err)
			}
			if turn.AssistantMessage.ID == uuid.Nil {
				t.Fatal("not persisted")
			}
		})
	}
}

func TestDocumentQuestionUsesAuthoritativeVisibleFields(t *testing.T) {
	d := domain.TemplateDraft{Fields: []domain.TemplateField{{Key: "hidden", Question: "Не спрашивать"}, {Key: "conditional", Question: "Вопрос из профиля"}}, Validation: &domain.TemplateValidation{VisibleFields: []string{"conditional"}, RequiredFields: []string{"conditional"}, NextQuestion: &domain.TemplateField{Key: "conditional", Question: "Вопрос из профиля"}}}
	if len(activeDocumentFields(d)) != 1 || documentQuestion(d, "hidden") != "Вопрос из профиля" {
		t.Fatal("conditions independently reinterpreted")
	}
}

func TestDocumentRevokedTemplateDoesNotBlockRAG(t *testing.T) {
	ctx := context.Background()
	actor := domain.User{ID: uuid.New(), IsActive: true, Role: domain.RoleMember}
	store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
	search := &chatSearchStub{}
	chat := NewChatService(store, search, zap.NewNop())
	session, _ := chat.CreateSession(ctx, actor, "")
	draftID := uuid.New()
	repo := &documentRepoMemory{templateRepoFake: &templateRepoFake{
		template: domain.DocumentTemplate{ID: uuid.New(), Enabled: true},
		draft:    domain.TemplateDraft{ID: draftID, OwnerID: actor.ID},
	}}
	templates := NewTemplateService(repo, nil, &templateAccessFake{denied: true}, nil, nil, zap.NewNop())
	model := &interpreterStub{}
	flow := &workflowMemory{flow: &domain.ChatDocumentWorkflow{SessionID: session.ID, OwnerID: actor.ID, DraftID: &draftID, Stage: "collecting_fields", Version: 1}}
	chat.EnableDocuments(flow, templates, model)
	if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.New(), "Каковы общие правила регистрации?"); err != nil {
		t.Fatal(err)
	}
	if search.calls != 1 || model.calls != 0 || flow.flow.Version != 1 {
		t.Fatal("revoked workflow blocked RAG or sent private data to the interpreter")
	}
}

func TestDocumentSelectionRequiresChoiceForAmbiguousOrMissingMatch(t *testing.T) {
	for _, matches := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(matches), func(t *testing.T) {
			ctx := context.Background()
			actor := domain.User{ID: uuid.New(), IsActive: true, Role: domain.RoleMember}
			store := &chatMemory{turns: map[uuid.UUID]domain.ChatTurn{}}
			chat := NewChatService(store, &chatSearchStub{}, zap.NewNop())
			session, _ := chat.CreateSession(ctx, actor, "")
			repo := &documentRepoMemory{templateRepoFake: &templateRepoFake{}}
			ids := []string{}
			for i := 0; i < 2; i++ {
				template := domain.DocumentTemplate{ID: uuid.New(), Name: fmt.Sprint(i), Enabled: true,
					Fields: []domain.TemplateField{{Key: "purpose", Label: "Цель", Question: "Какова цель?", Type: "text", Required: true, MaxLength: 100}}}
				repo.catalog = append(repo.catalog, template)
				if i < matches {
					ids = append(ids, template.ID.String())
				}
			}
			flow := &workflowMemory{}
			model := &interpreterStub{plan: aiclient.DocumentInterpretation{Intent: "create_document", TemplateIDs: ids, Changes: map[string]json.RawMessage{}}}
			model.plan.NeedsClarification = matches == 1
			chat.EnableDocuments(flow, NewTemplateService(repo, nil, &templateAccessFake{}, nil, nil, zap.NewNop()), model)
			if _, err := chat.SendMessage(ctx, actor, session.ID, uuid.New(), "Подготовь документ"); err != nil {
				t.Fatal(err)
			}
			v, err := chat.DocumentView(ctx, actor, session.ID)
			optionCount := 2
			if matches == 1 {
				optionCount = 1
			}
			if err != nil || repo.creates != 0 || v.Workflow.Stage != "selecting_template" || len(v.Options) != optionCount {
				t.Fatal("ambiguous or absent match created a guessed draft", err)
			}
			_, err = chat.SendDocumentAction(ctx, actor, session.ID, ChatDocumentAction{Action: "choose", TemplateID: repo.catalog[1].ID, WorkflowVersion: v.Workflow.Version, ClientMessageID: uuid.New()})
			if err != nil || repo.creates != 1 {
				t.Fatal("explicit template choice failed", err)
			}
		})
	}
}
