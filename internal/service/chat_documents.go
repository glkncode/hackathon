package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatDocumentStore interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (domain.ChatDocumentWorkflow, error)
	Save(context.Context, domain.ChatDocumentWorkflow) error
	Atomic(context.Context, uuid.UUID, uuid.UUID, func(context.Context) error) error
}
type DocumentInterpreter interface {
	InterpretDocument(context.Context, aiclient.DocumentInterpretRequest) (aiclient.DocumentInterpretation, error)
}
type chatDocuments struct {
	store     ChatDocumentStore
	templates *TemplateService
	model     DocumentInterpreter
}

func (s *ChatService) EnableDocuments(store ChatDocumentStore, templates *TemplateService, model DocumentInterpreter) {
	s.documents = &chatDocuments{store, templates, model}
}

type ChatDocumentAction struct {
	ClientMessageID  uuid.UUID                  `json:"client_message_id"`
	Action           string                     `json:"action"`
	TemplateID       uuid.UUID                  `json:"template_id,omitempty"`
	ExpectedRevision int64                      `json:"expected_revision"`
	WorkflowVersion  int64                      `json:"workflow_version"`
	FieldKey         string                     `json:"field_key,omitempty"`
	Values           map[string]json.RawMessage `json:"values,omitempty"`
}

func (s *ChatService) documentCatalog(ctx context.Context, actor domain.User) ([]domain.ChatTemplateOption, error) {
	out := []domain.ChatTemplateOption{}
	// A bounded model catalogue. Never fall back to profiles on disk or templates
	// omitted by the ownership/ACL-aware repository query.
	for offset := 0; offset < 200; offset += 100 {
		ts, err := s.documents.templates.ListTemplates(ctx, actor, 100, offset)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			out = append(out, domain.ChatTemplateOption{ID: t.ID, Name: t.Name, Description: t.Description})
		}
		if len(ts) < 100 {
			break
		}
	}
	return out, nil
}
func (s *ChatService) DocumentView(ctx context.Context, actor domain.User, session uuid.UUID) (domain.ChatDocumentView, error) {
	view := domain.ChatDocumentView{Enabled: s.documents != nil}
	if _, err := s.repository.GetSessionByID(ctx, actor.ID, session); err != nil {
		return view, err
	}
	if s.documents == nil {
		return view, nil
	}
	f, err := s.documents.store.Get(ctx, actor.ID, session)
	if errors.Is(err, domain.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.Workflow = &f
	if f.DraftID != nil {
		d, err := s.documents.templates.GetDraft(ctx, actor, *f.DraftID)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrAccessDenied) {
			view.Workflow = nil
			view.Unavailable = true
			return view, nil
		}
		if err != nil {
			return view, err
		}
		view.Draft = &d
		t, err := s.documents.templates.GetTemplate(ctx, actor, d.TemplateID)
		if err != nil {
			return view, err
		}
		view.TemplateName = t.Name
		if f.GeneratedID != nil {
			g, err := s.documents.templates.GetGeneratedDocument(ctx, actor, *f.GeneratedID)
			if err != nil {
				return view, err
			}
			view.Generated = &g
		}
	}
	if f.Stage == "selecting_template" {
		options, err := s.documentCatalog(ctx, actor)
		if err != nil {
			return view, err
		}
		for _, o := range options {
			if len(f.CandidateIDs) == 0 || slices.Contains(f.CandidateIDs, o.ID) {
				view.Options = append(view.Options, o)
			}
		}
	}
	return view, nil
}

func draftValidation(d domain.TemplateDraft) domain.TemplateValidation {
	if d.Validation != nil {
		return *d.Validation
	}
	return domain.ValidateTemplateValues(d.Fields, d.Values)
}
func activeDocumentFields(d domain.TemplateDraft) []domain.TemplateField {
	v := draftValidation(d)
	out := []domain.TemplateField{}
	for _, f := range d.Fields {
		if v.VisibleFields == nil || slices.Contains(v.VisibleFields, f.Key) {
			out = append(out, f)
		}
	}
	return out
}
func documentQuestion(d domain.TemplateDraft, expected string) string {
	for _, f := range activeDocumentFields(d) {
		if f.Key == expected {
			return f.Question
		}
	}
	v := draftValidation(d)
	if v.NextQuestion != nil {
		return v.NextQuestion.Question
	}
	return "Обязательные поля заполнены. Проверьте данные и предупреждения перед созданием проекта DOCX."
}
func updateDocumentStage(f *domain.ChatDocumentWorkflow, d domain.TemplateDraft) {
	f.ReviewedRevision = 0
	f.GeneratedID = nil
	f.ExpectedField = ""
	f.Stage = "collecting_fields"
	v := draftValidation(d)
	if v.Ready {
		f.Stage = "reviewing"
		f.ReviewedRevision = d.Revision
	}
}

// Interpret once per natural-language turn, with only current authorized data.
// No RAG calls occur on the field-answer path. Questions fall through unchanged.
func (s *ChatService) routeDocumentMessage(ctx context.Context, actor domain.User, session, key uuid.UUID, text string) (domain.ChatTurn, bool, error) {
	view, err := s.DocumentView(ctx, actor, session)
	if err != nil {
		return domain.ChatTurn{}, true, err
	}
	if view.Unavailable {
		// Revoking template access disables this workflow, but must not disable
		// ordinary knowledge questions in the same chat. Do not send its data.
		return domain.ChatTurn{}, false, nil
	}
	options, err := s.documentCatalog(ctx, actor)
	if err != nil {
		return domain.ChatTurn{}, true, err
	}
	req := aiclient.DocumentInterpretRequest{Query: text, Templates: options, Fields: []domain.TemplateField{}, Values: map[string]json.RawMessage{}}
	if view.Workflow != nil {
		req.Stage = view.Workflow.Stage
		req.ExpectedField = view.Workflow.ExpectedField
	}
	if view.Draft != nil && view.Workflow.Stage != "cancelled" {
		req.Fields = activeDocumentFields(*view.Draft)
		for i := range req.Fields {
			f := &req.Fields[i]
			f.Bindings = nil
			f.Source = nil
			f.RequiredIf = nil
			f.VisibleIf = nil
			if v := draftValidation(*view.Draft); v.RequiredFields != nil {
				f.Required = slices.Contains(v.RequiredFields, f.Key)
			}
			if value, ok := view.Draft.Values[f.Key]; ok {
				req.Values[f.Key] = value
			}
		}
		if req.ExpectedField == "" {
			if next := draftValidation(*view.Draft).NextQuestion; next != nil {
				req.ExpectedField = next.Key
			}
		}
	}
	start := time.Now()
	modelctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var plan aiclient.DocumentInterpretation
	if s.documents.model == nil {
		err = domain.ErrTemplateEngine
	} else {
		plan, err = s.documents.model.InterpretDocument(modelctx, req)
	}
	s.log.Info("chat document interpretation", zap.Duration("duration", time.Since(start)), zap.Bool("success", err == nil))
	if err == nil {
		err = plan.Validate(req)
	}
	if err != nil {
		if ctx.Err() != nil {
			return domain.ChatTurn{}, true, ctx.Err()
		}
		if view.Workflow == nil {
			return domain.ChatTurn{}, false, nil
		}
		plan = aiclient.DocumentInterpretation{Intent: "ambiguous", TemplateIDs: []string{}, Changes: map[string]json.RawMessage{}}
	}
	if plan.Intent == "knowledge_question" {
		return domain.ChatTurn{}, false, nil
	}
	a := ChatDocumentAction{ClientMessageID: key, Action: plan.Intent, FieldKey: plan.TargetField, Values: plan.Changes}
	if view.Workflow != nil {
		a.WorkflowVersion = view.Workflow.Version
	}
	if view.Draft != nil {
		a.ExpectedRevision = view.Draft.Revision
	}
	turn, err := s.applyDocumentAction(ctx, actor, session, a, text, "", plan)
	return turn, true, err
}

func (s *ChatService) SendDocumentAction(ctx context.Context, actor domain.User, session uuid.UUID, a ChatDocumentAction) (domain.ChatTurn, error) {
	if err := chatActor(actor); err != nil {
		return domain.ChatTurn{}, err
	}
	if s.documents == nil {
		return domain.ChatTurn{}, domain.ErrNotFound
	}
	if a.ClientMessageID == uuid.Nil {
		return domain.ChatTurn{}, domain.ErrInvalidInput
	}
	labels := map[string]string{"start": "Начать подготовку документа", "choose": "Выбрать шаблон", "answer": "Сохранить ответ", "edit": "Изменить поле", "progress": "Показать заполнение", "validate": "Проверить документ", "generate": "Создать DOCX", "pause": "Продолжить позже", "resume": "Продолжить заполнение", "cancel": "Завершить подготовку без удаления черновика"}
	label, ok := labels[a.Action]
	if !ok {
		return domain.ChatTurn{}, domain.ErrInvalidInput
	}
	if (a.Action == "answer" || a.Action == "edit" || a.Action == "generate") && a.ExpectedRevision < 1 {
		return domain.ChatTurn{}, domain.ErrInvalidInput
	}
	payload, _ := json.Marshal(a)
	hash := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(hash[:])
	previous, err := s.repository.GetTurnByClientID(ctx, actor.ID, session, a.ClientMessageID)
	if err == nil {
		if previous.UserMessage.DocumentActionHash != fingerprint {
			return domain.ChatTurn{}, domain.ErrChatConflict
		}
		return previous, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.ChatTurn{}, err
	}
	view, err := s.DocumentView(ctx, actor, session)
	if err != nil {
		return domain.ChatTurn{}, err
	}
	if view.Unavailable {
		return domain.ChatTurn{}, domain.ErrNotFound
	}
	plan := aiclient.DocumentInterpretation{TemplateIDs: []string{}, Changes: a.Values}
	return s.applyDocumentAction(ctx, actor, session, a, label, fingerprint, plan)
}

func (s *ChatService) applyDocumentAction(ctx context.Context, actor domain.User, session uuid.UUID, a ChatDocumentAction, text, fingerprint string, plan aiclient.DocumentInterpretation) (turn domain.ChatTurn, err error) {
	err = s.documents.store.Atomic(ctx, actor.ID, session, func(txctx context.Context) error {
		previous, e := s.repository.GetTurnByClientID(txctx, actor.ID, session, a.ClientMessageID)
		if e == nil {
			if previous.UserMessage.Content != text || previous.UserMessage.DocumentActionHash != fingerprint {
				return domain.ErrChatConflict
			}
			turn = previous
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		f, e := s.documents.store.Get(txctx, actor.ID, session)
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if errors.Is(e, domain.ErrNotFound) {
			f = domain.ChatDocumentWorkflow{OwnerID: actor.ID, SessionID: session, Stage: "selecting_template"}
		}
		if f.Version != a.WorkflowVersion {
			return domain.ErrRevisionConflict
		}
		var d domain.TemplateDraft
		if f.DraftID != nil {
			d, e = s.documents.templates.GetDraft(txctx, actor, *f.DraftID)
			if e != nil {
				return e
			}
			if a.ExpectedRevision != 0 && d.Revision != a.ExpectedRevision {
				return domain.ErrRevisionConflict
			}
		}
		message := ""
		save := true
		switch a.Action {
		case "start", "create_document", "choose":
			// Starting over detaches the old draft, preserving it in the Wizard.
			f.DraftID = nil
			f.GeneratedID = nil
			f.ExpectedField = ""
			f.ReviewedRevision = 0
			f.Stage = "selecting_template"
			f.CandidateIDs = nil
			chosen := a.TemplateID
			if a.Action == "create_document" {
				for _, id := range plan.TemplateIDs {
					parsed, err := uuid.Parse(id)
					if err != nil {
						return domain.ErrInvalidInput
					}
					if _, err = s.documents.templates.GetTemplate(txctx, actor, parsed); err != nil {
						return err
					}
					f.CandidateIDs = append(f.CandidateIDs, parsed)
				}
				if len(f.CandidateIDs) == 1 && !plan.NeedsClarification {
					chosen = f.CandidateIDs[0]
				}
			}
			if chosen != uuid.Nil {
				t, err := s.documents.templates.GetTemplate(txctx, actor, chosen)
				if err != nil {
					return err
				}
				d, err = s.documents.templates.CreateDraft(txctx, actor, t.ID)
				if err != nil {
					return err
				}
				f.DraftID = &d.ID
				f.CandidateIDs = nil
				updateDocumentStage(&f, d)
				message = "Подойдёт шаблон «" + t.Name + "». Начнём заполнение.\n\n" + documentQuestion(d, "")
			} else if a.Action == "create_document" && len(f.CandidateIDs) == 0 {
				message = "Среди доступных шаблонов не нашёл подходящего. Уточните назначение документа или выберите шаблон из каталога."
			} else {
				message = "Выберите подходящий шаблон для подготовки документа."
			}
		case "answer", "answer_field", "edit_document_field":
			if f.DraftID == nil || f.Stage == "paused" || f.Stage == "cancelled" {
				message = "Сначала начните или продолжите подготовку документа."
				break
			}
			if plan.NeedsClarification {
				message = "Не удалось однозначно определить ответ. Уточните данные или воспользуйтесь полем ввода под сообщениями.\n\n" + documentQuestion(d, f.ExpectedField)
				break
			}
			if len(a.Values) == 0 {
				if a.FieldKey != "" {
					valid := false
					for _, field := range activeDocumentFields(d) {
						if field.Key == a.FieldKey {
							valid = true
						}
					}
					if !valid {
						return domain.ErrInvalidInput
					}
					f.ExpectedField = a.FieldKey
				}
				message = documentQuestion(d, f.ExpectedField)
				break
			}
			allowed := map[string]bool{}
			for _, field := range activeDocumentFields(d) {
				allowed[field.Key] = true
			}
			for key := range a.Values {
				if !allowed[key] {
					return domain.ErrInvalidInput
				}
			}
			updated, err := s.documents.templates.UpdateDraftFields(txctx, actor, d.ID, d.Revision, a.Values)
			if errors.Is(err, domain.ErrInvalidInput) {
				message = "Ответ не принят. Проверьте формат, дату и заполнение строк. Сохранённые данные не изменены.\n\n" + documentQuestion(d, f.ExpectedField)
				break
			}
			if err != nil {
				return err
			}
			d = updated
			updateDocumentStage(&f, d)
			message = "Ответ сохранён.\n\n" + documentQuestion(d, "")
		case "edit":
			if f.DraftID == nil {
				return domain.ErrInvalidInput
			}
			found := false
			for _, field := range activeDocumentFields(d) {
				if field.Key == a.FieldKey {
					found = true
				}
			}
			if !found {
				return domain.ErrInvalidInput
			}
			f.ExpectedField = a.FieldKey
			f.Stage = "collecting_fields"
			f.ReviewedRevision = 0
			message = documentQuestion(d, f.ExpectedField)
		case "validate", "validate_document", "progress", "show_document_progress":
			if f.DraftID == nil {
				message = "Сначала выберите шаблон."
				break
			}
			updateDocumentStage(&f, d)
			v := draftValidation(d)
			message = fmt.Sprintf("Заполнено обязательных полей: %d из %d. Данные и результаты проверки показаны ниже. Документ является проектом для дальнейшей проверки.", d.CompletedFieldCount, d.RequiredFieldCount)
			if v.Ready {
				message += "\n\nПроверка заполнения завершена. Сформировать проект документа?"
			} else {
				message += "\n\n" + documentQuestion(d, "")
			}
		case "generate", "generate_document":
			if f.DraftID == nil {
				return domain.ErrInvalidInput
			}
			v := draftValidation(d)
			if !v.Ready {
				updateDocumentStage(&f, d)
				message = "Пока нельзя сформировать документ. Исправьте обязательные данные и ошибки проверки.\n\n" + documentQuestion(d, "")
				break
			}
			if f.ReviewedRevision != d.Revision || (f.Stage != "reviewing" && f.Stage != "completed") {
				updateDocumentStage(&f, d)
				message = "Проверьте актуальные данные и предупреждения ниже, затем подтвердите создание DOCX."
				break
			}
			// Existing generation commits an immutable artifact independently. If the
			// outer turn fails, retry returns that same (draft,revision) artifact.
			g, err := s.documents.templates.GenerateDocument(txctx, actor, d.ID, d.Revision)
			if err != nil {
				return err
			}
			f.GeneratedID = &g.ID
			f.Stage = "completed"
			message = "Проект документа сформирован. Вы можете скачать файл и проверить его перед подписанием."
		case "pause", "pause_document":
			if f.DraftID == nil {
				return domain.ErrInvalidInput
			}
			f.Stage = "paused"
			message = "Подготовка отложена. Черновик сохранён; можно продолжить в этом чате или открыть его в мастере."
		case "cancel", "cancel_document_flow":
			f.Stage = "cancelled"
			message = "Подготовка завершена. Сохранённый черновик остаётся в разделе «Шаблоны»."
		case "resume", "continue_document":
			if f.DraftID == nil {
				message = "Выберите шаблон, чтобы начать подготовку."
				break
			}
			updateDocumentStage(&f, d)
			message = "Продолжим заполнение.\n\n" + documentQuestion(d, f.ExpectedField)
		case "template_catalog":
			// Names are projected with fresh ACL in DocumentView rather than
			// copied into history where revocation could not retract them.
			if f.DraftID == nil {
				f.Stage = "selecting_template"
				f.CandidateIDs = nil
				message = "Доступные зарегистрированные шаблоны показаны ниже. Выбор шаблона начнёт подготовку документа."
			} else {
				message = "Каталог доступен в разделе «Шаблоны». Текущий черновик сохранён; можно продолжить его заполнение."
				save = false
			}
		case "ambiguous":
			message = "Хотите, чтобы я помог подготовить документ по существующему шаблону? Нажмите «Начать подготовку» или продолжите задавать вопросы."
			if f.DraftID != nil {
				message = "Уточните, какие сведения нужно внести или изменить. Можно воспользоваться полем ввода ниже.\n\n" + documentQuestion(d, f.ExpectedField)
			}
			save = false
		default:
			return domain.ErrInvalidInput
		}
		if save {
			if err := s.documents.store.Save(txctx, f); err != nil {
				return err
			}
		}
		mode := domain.ChatModeDocument
		pending := domain.ChatTurn{UserMessage: domain.ChatMessage{Role: domain.ChatRoleUser, Content: text, DocumentDraftID: f.DraftID, DocumentActionHash: fingerprint}, AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: message, Mode: &mode, DocumentDraftID: f.DraftID}}
		if f.Stage == "completed" {
			pending.AssistantMessage.DocumentGeneratedID = f.GeneratedID
		}
		turn, e = s.repository.SaveMessageWithSources(txctx, actor.ID, session, a.ClientMessageID, pending, nil)
		return e
	})
	return turn, err
}
