package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type templateRepoFake struct {
	Templates
	template   domain.DocumentTemplate
	draft      domain.TemplateDraft
	generated  domain.GeneratedDocument
	persistErr error
}

func (r *templateRepoFake) GetTemplate(context.Context, uuid.UUID) (domain.DocumentTemplate, error) {
	return r.template, nil
}
func (r *templateRepoFake) GetDraft(_ context.Context, owner, id uuid.UUID) (domain.TemplateDraft, error) {
	if r.draft.OwnerID != owner || r.draft.ID != id {
		return domain.TemplateDraft{}, domain.ErrNotFound
	}
	return r.draft, nil
}
func (r *templateRepoFake) UpdateDraft(_ context.Context, d domain.TemplateDraft, revision int64) (domain.TemplateDraft, error) {
	d.Revision = revision + 1
	r.draft = d
	return d, nil
}
func (r *templateRepoFake) GetGenerated(_ context.Context, owner, id uuid.UUID) (domain.GeneratedDocument, error) {
	if r.generated.OwnerID != owner || r.generated.ID != id {
		return domain.GeneratedDocument{}, domain.ErrNotFound
	}
	return r.generated, nil
}
func (r *templateRepoFake) Generate(ctx context.Context, owner, id uuid.UUID, revision int64, build func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error)) (domain.GeneratedDocument, error) {
	g, err := build(ctx, r.draft)
	if err != nil {
		return g, err
	}
	if r.persistErr != nil {
		return g, r.persistErr
	}
	g.ID = uuid.New()
	g.OwnerID = owner
	g.DraftID = id
	r.generated = g
	return g, nil
}

type templateVersionFake struct{ version domain.DocumentVersion }

func (r templateVersionFake) GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return r.version, nil
}

type templateAccessFake struct{ denied bool }

func (a *templateAccessFake) Check(context.Context, domain.User, uuid.UUID) error {
	if a.denied {
		return domain.ErrNotFound
	}
	return nil
}

type templateEngineFake struct {
	TemplateEngine
	output []byte
	err    error
	after  func()
	calls  int
}

func (e *templateEngineFake) InspectTemplate(context.Context, []byte, []domain.TemplateField, map[string]json.RawMessage) (aiclient.TemplateInspection, error) {
	return aiclient.TemplateInspection{}, e.err
}
func (e *templateEngineFake) RenderTemplate(context.Context, []byte, []domain.TemplateField, map[string]json.RawMessage) ([]byte, error) {
	e.calls++
	if e.after != nil {
		e.after()
	}
	return e.output, e.err
}
func TestTemplateServiceOwnershipACLGenerationCleanup(t *testing.T) {
	for _, mode := range []string{"success", "foreign", "disabled", "acl_revoked", "revoked_during_render", "python_error", "persistence_error", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
			dir := t.TempDir()
			storage, err := NewLocalDocumentStorage(dir, domain.MaxGeneratedBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			source, err := storage.Save(ctx, bytes.NewReader(docxBytes(t, "")), "source.docx")
			if err != nil {
				t.Fatal(err)
			}
			repo := &templateRepoFake{template: domain.DocumentTemplate{ID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(), Enabled: true}}
			repo.draft = domain.TemplateDraft{ID: uuid.New(), TemplateID: repo.template.ID, DocumentVersionID: repo.template.DocumentVersionID, OwnerID: actor.ID, Revision: 1, Fields: []domain.TemplateField{{Key: "person", Type: "person", Required: true, MaxLength: 100}}, Values: map[string]json.RawMessage{"person": json.RawMessage(`"Иванов"`)}}
			access := &templateAccessFake{}
			engine := &templateEngineFake{output: docxBytes(t, "")}
			switch mode {
			case "foreign":
				actor.ID = uuid.New()
			case "disabled":
				actor.IsActive = false
			case "acl_revoked":
				access.denied = true
			case "revoked_during_render":
				engine.after = func() { access.denied = true }
			case "python_error":
				engine.err = domain.ErrTemplateEngine
			case "persistence_error":
				repo.persistErr = errors.New("db failure")
			case "incomplete":
				repo.draft.Values = map[string]json.RawMessage{}
			}
			svc := NewTemplateService(repo, templateVersionFake{domain.DocumentVersion{ID: repo.template.DocumentVersionID, DocumentID: repo.template.DocumentID, StorageKey: source.Key, MIMEType: domain.DOCXMIME, SizeBytes: source.Size}}, access, storage, engine, zap.NewNop())
			generated, err := svc.GenerateDocument(ctx, actor, repo.draft.ID, 1)
			files, _ := os.ReadDir(dir)
			if mode == "success" {
				if err != nil || generated.ID == uuid.Nil || len(files) != 2 {
					t.Fatal("generation failed", err)
				}
				access.denied = true
				if _, _, err := svc.DownloadGeneratedDocument(ctx, actor, generated.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("download after revoke permitted")
				}
				access.denied = false
				foreign := actor
				foreign.ID = uuid.New()
				foreign.Role = domain.RoleAdmin
				if _, err := svc.GetGeneratedDocument(ctx, foreign, generated.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("admin foreign draft exposed")
				}
			} else {
				if err == nil || len(files) != 1 {
					t.Fatalf("failed generation leaked success/file: %v files=%d", err, len(files))
				}
			}
		})
	}
}
func TestTemplateAnswersAreBoundToQuestionAndRevision(t *testing.T) {
	actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	repo := &templateRepoFake{template: domain.DocumentTemplate{ID: uuid.New(), DocumentVersionID: uuid.New(), Enabled: true}}
	repo.draft = domain.TemplateDraft{ID: uuid.New(), OwnerID: actor.ID, TemplateID: repo.template.ID, DocumentVersionID: repo.template.DocumentVersionID, Revision: 1, Values: map[string]json.RawMessage{}, Fields: []domain.TemplateField{{Key: "leader", Type: "person", Required: true, MaxLength: 100}, {Key: "signer", Type: "person", Required: true, MaxLength: 100}}}
	svc := NewTemplateService(repo, nil, &templateAccessFake{}, nil, nil, zap.NewNop())
	if _, err := svc.SubmitAnswer(context.Background(), actor, repo.draft.ID, 1, "signer", json.RawMessage(`"Иванов"`)); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatal("ambiguous answer accepted")
	}
	if _, err := svc.SubmitAnswer(context.Background(), actor, repo.draft.ID, 1, "leader", json.RawMessage(`"Иванов"`)); err != nil {
		t.Fatal(err)
	}
	v, err := svc.GetNextQuestion(context.Background(), actor, repo.draft.ID)
	if err != nil || v.NextQuestion.Key != "signer" {
		t.Fatal("wrong next question")
	}
	if _, err := svc.UpdateDraftFields(context.Background(), actor, repo.draft.ID, 1, map[string]json.RawMessage{"signer": json.RawMessage(`"Петров"`)}); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatal("stale revision accepted")
	}
}
