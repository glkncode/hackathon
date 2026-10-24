package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Templates interface {
	CreateTemplate(context.Context, domain.DocumentTemplate) (domain.DocumentTemplate, error)
	GetTemplate(context.Context, uuid.UUID) (domain.DocumentTemplate, error)
	ListTemplates(context.Context, uuid.UUID, int, int) ([]domain.DocumentTemplate, error)
	SetTemplateEnabled(context.Context, uuid.UUID, bool) (domain.DocumentTemplate, error)
	CreateDraft(context.Context, uuid.UUID, domain.DocumentTemplate) (domain.TemplateDraft, error)
	GetDraft(context.Context, uuid.UUID, uuid.UUID) (domain.TemplateDraft, error)
	ListDrafts(context.Context, uuid.UUID, int, int) ([]domain.TemplateDraft, error)
	UpdateDraft(context.Context, domain.TemplateDraft, int64) (domain.TemplateDraft, error)
	GetGenerated(context.Context, uuid.UUID, uuid.UUID) (domain.GeneratedDocument, error)
	Generate(context.Context, uuid.UUID, uuid.UUID, int64, func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error)) (domain.GeneratedDocument, error)
}
type TemplateEngine interface {
	InspectTemplate(context.Context, []byte, []domain.TemplateField, map[string]json.RawMessage) (aiclient.TemplateInspection, error)
	RenderTemplate(context.Context, []byte, []domain.TemplateField, map[string]json.RawMessage) ([]byte, error)
}
type ProfileTemplateEngine interface {
	InspectProfile(context.Context, []byte, json.RawMessage, map[string]json.RawMessage) (aiclient.TemplateInspection, error)
	RenderProfile(context.Context, []byte, json.RawMessage, map[string]json.RawMessage) ([]byte, error)
}
type TemplateVersions interface {
	GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error)
}
type TemplateService struct {
	repo     Templates
	versions TemplateVersions
	access   DocumentAuthorization
	storage  DocumentStorage
	engine   TemplateEngine
	slots    chan struct{}
	log      *zap.Logger
}

func NewTemplateService(repo Templates, versions TemplateVersions, access DocumentAuthorization, storage DocumentStorage, engine TemplateEngine, log *zap.Logger) *TemplateService {
	return &TemplateService{repo: repo, versions: versions, access: access, storage: storage, engine: engine, slots: make(chan struct{}, 4), log: log}
}
func templateActor(actor domain.User) error {
	if actor.ID == uuid.Nil || !actor.IsActive || !actor.Role.Valid() {
		return domain.ErrNotFound
	}
	return nil
}
func (s *TemplateService) source(ctx context.Context, t domain.DocumentTemplate) ([]byte, error) {
	v, err := s.versions.GetVersionByID(ctx, t.DocumentVersionID)
	if err != nil {
		return nil, err
	}
	if v.DocumentID != t.DocumentID || v.MIMEType != domain.DOCXMIME {
		return nil, domain.ErrInvalidInput
	}
	if v.SizeBytes > domain.MaxTemplateBytes {
		return nil, ErrFileTooLarge
	}
	f, err := s.storage.Open(v.StorageKey)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: f}, domain.MaxTemplateBytes+1))
	if len(data) > domain.MaxTemplateBytes {
		return nil, ErrFileTooLarge
	}
	return data, err
}
func (s *TemplateService) Register(ctx context.Context, actor domain.User, t domain.DocumentTemplate) (domain.DocumentTemplate, error) {
	if err := requireAdmin(actor); err != nil {
		return t, err
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.DocumentID == uuid.Nil || t.DocumentVersionID == uuid.Nil || !templateMetadata(t.Name, 255) || t.Name == "" || !templateMetadata(t.Description, 2000) {
		return t, domain.ErrInvalidInput
	}
	if err := s.access.Check(ctx, actor, t.DocumentID); err != nil {
		return t, err
	}
	content, err := s.source(ctx, t)
	if err != nil {
		return t, err
	}
	if s.engine == nil {
		return t, domain.ErrTemplateEngine
	}
	if len(t.Profile) > 0 {
		if t.Fields != nil {
			return t, domain.ErrInvalidInput
		}
		t.Fields, err = domain.ProfileFields(t.Profile)
		if err != nil {
			return t, err
		}
		engine, ok := s.engine.(ProfileTemplateEngine)
		if !ok {
			return t, domain.ErrTemplateEngine
		}
		if _, err = engine.InspectProfile(ctx, content, t.Profile, nil); err != nil {
			return t, err
		}
		t.SchemaVersion = 2
		return s.repo.CreateTemplate(ctx, t)
	}
	t.SchemaVersion = 1
	if t.Fields != nil {
		if err = domain.ValidateTemplateSchema(t.Fields); err != nil {
			return t, err
		}
	}
	inspection, err := s.engine.InspectTemplate(ctx, content, t.Fields, nil)
	if err != nil {
		return t, err
	}
	if t.Fields == nil {
		if len(inspection.Fields) == 0 {
			return t, domain.ErrTemplateNeedsConfiguration
		}
		for _, field := range inspection.Fields {
			t.Fields = append(t.Fields, domain.TemplateField{Key: field.Name, Label: field.Name, Type: "text", Required: field.Required, Question: "Укажите значение: " + field.Name, MaxLength: 8000})
		}
	}
	if err = domain.ValidateTemplateSchema(t.Fields); err != nil {
		return t, err
	}
	return s.repo.CreateTemplate(ctx, t)
}
func templateMetadata(value string, max int) bool {
	if !templateString(value, max) {
		return false
	}
	return !strings.ContainsAny(value, "\r\n\t")
}
func templateString(value string, max int) bool {
	f := []domain.TemplateField{{Key: "x", Type: "text", MaxLength: max}}
	raw, _ := json.Marshal(value)
	return len(domain.ValidateTemplateValues(f, map[string]json.RawMessage{"x": raw}).InvalidFields) == 0
}
func (s *TemplateService) GetTemplate(ctx context.Context, actor domain.User, id uuid.UUID) (domain.DocumentTemplate, error) {
	if err := templateActor(actor); err != nil {
		return domain.DocumentTemplate{}, err
	}
	t, err := s.repo.GetTemplate(ctx, id)
	if err != nil {
		return t, err
	}
	if !t.Enabled {
		return t, domain.ErrNotFound
	}
	return t, s.access.Check(ctx, actor, t.DocumentID)
}
func (s *TemplateService) SetEnabled(ctx context.Context, actor domain.User, id uuid.UUID, enabled bool) (domain.DocumentTemplate, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.DocumentTemplate{}, err
	}
	return s.repo.SetTemplateEnabled(ctx, id, enabled)
}
func (s *TemplateService) ListTemplates(ctx context.Context, actor domain.User, limit, offset int) ([]domain.DocumentTemplate, error) {
	if err := templateActor(actor); err != nil {
		return nil, err
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, err
	}
	return s.repo.ListTemplates(ctx, actor.ID, limit, offset)
}
func (s *TemplateService) CreateDraft(ctx context.Context, actor domain.User, id uuid.UUID) (domain.TemplateDraft, error) {
	t, err := s.GetTemplate(ctx, actor, id)
	if err != nil {
		return domain.TemplateDraft{}, err
	}
	d, err := s.repo.CreateDraft(ctx, actor.ID, t)
	if err == nil && len(t.Profile) > 0 {
		return s.GetDraft(ctx, actor, d.ID)
	}
	return d, err
}
func (s *TemplateService) GetDraft(ctx context.Context, actor domain.User, id uuid.UUID) (domain.TemplateDraft, error) {
	if err := templateActor(actor); err != nil {
		return domain.TemplateDraft{}, err
	}
	d, err := s.repo.GetDraft(ctx, actor.ID, id)
	if err != nil {
		return d, err
	}
	if d.OwnerID != actor.ID {
		return domain.TemplateDraft{}, domain.ErrNotFound
	}
	t, err := s.GetTemplate(ctx, actor, d.TemplateID)
	if err != nil {
		return domain.TemplateDraft{}, err
	}
	if t.DocumentVersionID != d.DocumentVersionID {
		return domain.TemplateDraft{}, domain.ErrNotFound
	}
	if len(d.Profile) > 0 {
		v, err := s.validateProfile(ctx, t, d)
		if err != nil {
			return domain.TemplateDraft{}, err
		}
		applyProfileValidation(&d, v)
	}
	return d, nil
}

func applyProfileValidation(d *domain.TemplateDraft, v domain.TemplateValidation) {
	d.Validation = &v
	d.RequiredFieldCount = len(v.RequiredFields)
	d.CompletedFieldCount = d.RequiredFieldCount - len(v.MissingFields)
	for _, invalid := range v.InvalidFields {
		for _, key := range v.RequiredFields {
			if key == invalid.Key {
				d.CompletedFieldCount--
			}
		}
	}
	if d.CompletedFieldCount < 0 {
		d.CompletedFieldCount = 0
	}
	d.Status = "draft"
	if v.Ready {
		d.Status = "ready_to_generate"
	}
}
func (s *TemplateService) validateProfile(ctx context.Context, t domain.DocumentTemplate, d domain.TemplateDraft) (domain.TemplateValidation, error) {
	engine, ok := s.engine.(ProfileTemplateEngine)
	if !ok {
		return domain.TemplateValidation{}, domain.ErrTemplateEngine
	}
	content, err := s.source(ctx, t)
	if err != nil {
		return domain.TemplateValidation{}, err
	}
	inspection, err := engine.InspectProfile(ctx, content, d.Profile, d.Values)
	if err != nil {
		return domain.TemplateValidation{}, err
	}
	if inspection.Validation == nil {
		return domain.TemplateValidation{}, domain.ErrTemplateEngine
	}
	return *inspection.Validation, nil
}
func (s *TemplateService) ListDrafts(ctx context.Context, actor domain.User, limit, offset int) ([]domain.TemplateDraft, error) {
	if err := templateActor(actor); err != nil {
		return nil, err
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, err
	}
	drafts, err := s.repo.ListDrafts(ctx, actor.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i, d := range drafts {
		if len(d.Profile) > 0 {
			// Profile progress uses the same authority as validation/generation.
			drafts[i], err = s.GetDraft(ctx, actor, d.ID)
			if err != nil {
				return nil, err
			}
		}
	}
	return drafts, nil
}
func (s *TemplateService) UpdateDraftFields(ctx context.Context, actor domain.User, id uuid.UUID, revision int64, changes map[string]json.RawMessage) (domain.TemplateDraft, error) {
	d, err := s.GetDraft(ctx, actor, id)
	if err != nil {
		return d, err
	}
	if revision < 1 {
		return d, domain.ErrInvalidInput
	}
	if revision != d.Revision {
		return d, domain.ErrRevisionConflict
	}
	if len(changes) == 0 || len(changes) > 64 {
		return d, domain.ErrInvalidInput
	}
	for k, v := range changes {
		d.Values[k] = v
	}
	data, err := json.Marshal(d.Values)
	if err != nil || len(data) > 65536 {
		return d, ErrFileTooLarge
	}
	validation := domain.ValidateTemplateValues(d.Fields, d.Values)
	if len(d.Profile) > 0 {
		validation = domain.ValidateProfileValueTypes(d.Fields, d.Values)
	}
	if len(validation.InvalidFields) > 0 {
		return d, domain.ErrInvalidInput
	}
	if len(d.Profile) > 0 {
		t, err := s.GetTemplate(ctx, actor, d.TemplateID)
		if err != nil {
			return d, err
		}
		validation, err = s.validateProfile(ctx, t, d)
		if err != nil {
			return d, err
		}
		if len(validation.InvalidFields) > 0 {
			return d, domain.ErrInvalidInput
		}
		applyProfileValidation(&d, validation)
	}
	updated, err := s.repo.UpdateDraft(ctx, d, revision)
	if err == nil && len(d.Profile) > 0 {
		applyProfileValidation(&updated, validation)
	}
	return updated, err
}
func (s *TemplateService) SubmitAnswer(ctx context.Context, actor domain.User, id uuid.UUID, revision int64, key string, value json.RawMessage) (domain.TemplateDraft, error) {
	d, err := s.GetDraft(ctx, actor, id)
	if err != nil {
		return d, err
	}
	v := domain.ValidateTemplateValues(d.Fields, d.Values)
	if d.Validation != nil {
		v = *d.Validation
	}
	if v.NextQuestion == nil || v.NextQuestion.Key != key {
		return d, domain.ErrRevisionConflict
	}
	answer := domain.ValidateTemplateValues([]domain.TemplateField{*v.NextQuestion}, map[string]json.RawMessage{key: value})
	if len(d.Profile) > 0 {
		answer = domain.ValidateProfileValueTypes([]domain.TemplateField{*v.NextQuestion}, map[string]json.RawMessage{key: value})
		var decoded any
		_ = json.Unmarshal(value, &decoded)
		empty := decoded == nil
		switch x := decoded.(type) {
		case string:
			empty = strings.TrimSpace(x) == ""
		case []any:
			empty = len(x) == 0
		}
		if empty {
			answer.Ready = false
		}
	}
	if !answer.Ready {
		return d, domain.ErrInvalidInput
	}
	return s.UpdateDraftFields(ctx, actor, id, revision, map[string]json.RawMessage{key: value})
}
func (s *TemplateService) ValidateDraft(ctx context.Context, actor domain.User, id uuid.UUID) (domain.TemplateValidation, error) {
	d, err := s.GetDraft(ctx, actor, id)
	if err != nil {
		return domain.TemplateValidation{}, err
	}
	if d.Validation != nil {
		return *d.Validation, nil
	}
	return domain.ValidateTemplateValues(d.Fields, d.Values), nil
}
func (s *TemplateService) GetNextQuestion(ctx context.Context, actor domain.User, id uuid.UUID) (domain.TemplateValidation, error) {
	return s.ValidateDraft(ctx, actor, id)
}
func (s *TemplateService) GenerateDocument(ctx context.Context, actor domain.User, id uuid.UUID, revision int64) (domain.GeneratedDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	d, err := s.GetDraft(ctx, actor, id)
	if err != nil {
		return domain.GeneratedDocument{}, err
	}
	if revision < 1 {
		return domain.GeneratedDocument{}, domain.ErrInvalidInput
	}
	if revision != d.Revision {
		return domain.GeneratedDocument{}, domain.ErrRevisionConflict
	}
	v := domain.ValidateTemplateValues(d.Fields, d.Values)
	if d.Validation != nil {
		v = *d.Validation
	}
	if !v.Ready {
		return domain.GeneratedDocument{}, v
	}
	if s.engine == nil {
		return domain.GeneratedDocument{}, domain.ErrTemplateEngine
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return domain.GeneratedDocument{}, domain.ErrTemplateBusy
	}
	var saved *StoredFile
	result, err := s.repo.Generate(ctx, actor.ID, id, revision, func(ctx context.Context, locked domain.TemplateDraft) (domain.GeneratedDocument, error) {
		t, err := s.GetTemplate(ctx, actor, locked.TemplateID)
		if err != nil {
			return domain.GeneratedDocument{}, err
		}
		validation := domain.ValidateTemplateValues(locked.Fields, locked.Values)
		if len(locked.Profile) > 0 {
			validation, err = s.validateProfile(ctx, t, locked)
			if err != nil {
				return domain.GeneratedDocument{}, err
			}
		}
		if !validation.Ready {
			return domain.GeneratedDocument{}, validation
		}
		source, err := s.source(ctx, t)
		if err != nil {
			return domain.GeneratedDocument{}, err
		}
		var output []byte
		if len(locked.Profile) > 0 {
			engine, ok := s.engine.(ProfileTemplateEngine)
			if !ok {
				return domain.GeneratedDocument{}, domain.ErrTemplateEngine
			}
			output, err = engine.RenderProfile(ctx, source, locked.Profile, locked.Values)
		} else {
			output, err = s.engine.RenderTemplate(ctx, source, locked.Fields, locked.Values)
		}
		if err != nil {
			return domain.GeneratedDocument{}, err
		}
		if len(output) == 0 || len(output) > domain.MaxGeneratedBytes {
			return domain.GeneratedDocument{}, domain.ErrTemplateEngine
		}
		if len(locked.Profile) > 0 {
			check := domain.TemplateCheck{RuleID: "structure.output", Category: "blocking", Description: "Проверены ZIP/XML, защищённые зоны, нумерация и открытие результата через python-docx.", Result: "passed", Method: "structural_renderer"}
			for _, c := range validation.Checks {
				if c.RuleID == "structure.cleanup" {
					check.Source = c.Source
				}
			}
			validation.Checks = append(validation.Checks, check)
		}
		if _, err = s.GetTemplate(ctx, actor, locked.TemplateID); err != nil {
			return domain.GeneratedDocument{}, err
		}
		f, err := s.storage.Save(ctx, bytes.NewReader(output), "generated-document.docx")
		if err != nil {
			return domain.GeneratedDocument{}, err
		}
		saved = &f
		var snapshot json.RawMessage
		if len(locked.Profile) > 0 {
			snapshot, err = json.Marshal(map[string]any{"profile": locked.Profile, "values": locked.Values, "validation": validation, "source_document_version_id": locked.DocumentVersionID, "draft_revision": locked.Revision, "engine_version": validation.EngineVersion, "project_status": "draft_project"})
			if err != nil {
				return domain.GeneratedDocument{}, err
			}
		}
		return domain.GeneratedDocument{StorageKey: f.Key, Filename: f.Filename, MIMEType: f.MIMEType, SizeBytes: f.Size, SHA256: f.SHA256, GenerationSnapshot: snapshot}, nil
	})
	if err != nil && saved != nil && !errors.Is(err, domain.ErrGenerationCommitUnknown) {
		if cleanup := s.storage.Delete(saved.Key); cleanup != nil {
			s.log.Error("generated file cleanup failed", zap.Error(cleanup))
		}
	}
	return result, err
}
func (s *TemplateService) GetGeneratedDocument(ctx context.Context, actor domain.User, id uuid.UUID) (domain.GeneratedDocument, error) {
	if err := templateActor(actor); err != nil {
		return domain.GeneratedDocument{}, err
	}
	g, err := s.repo.GetGenerated(ctx, actor.ID, id)
	if err != nil {
		return g, err
	}
	if g.OwnerID != actor.ID {
		return domain.GeneratedDocument{}, domain.ErrNotFound
	}
	// Downloading an immutable result requires fresh ACL, never the rendering service.
	d, err := s.repo.GetDraft(ctx, actor.ID, g.DraftID)
	if err != nil {
		return g, err
	}
	if d.OwnerID != actor.ID {
		return g, domain.ErrNotFound
	}
	t, err := s.GetTemplate(ctx, actor, d.TemplateID)
	if err == nil && t.DocumentVersionID != d.DocumentVersionID {
		err = domain.ErrNotFound
	}
	return g, err
}
func (s *TemplateService) DownloadGeneratedDocument(ctx context.Context, actor domain.User, id uuid.UUID) (domain.GeneratedDocument, ReadSeekCloser, error) {
	g, err := s.GetGeneratedDocument(ctx, actor, id)
	if err != nil {
		return g, nil, err
	}
	f, err := s.storage.Open(g.StorageKey)
	return g, f, err
}

// Keep cancellation distinguishable for HTTP mapping without exposing transport details.
func TemplateEngineFailure(err error) bool {
	return errors.Is(err, domain.ErrTemplateEngine) || errors.Is(err, context.DeadlineExceeded)
}
