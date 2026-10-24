package service

import (
	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"testing"
)

type profileEngineFake struct {
	templateEngineFake
	validation  domain.TemplateValidation
	inspections int
}

func (e *profileEngineFake) InspectProfile(context.Context, []byte, json.RawMessage, map[string]json.RawMessage) (aiclient.TemplateInspection, error) {
	e.inspections++
	return aiclient.TemplateInspection{Validation: &e.validation}, e.err
}
func (e *profileEngineFake) RenderProfile(ctx context.Context, content []byte, _ json.RawMessage, values map[string]json.RawMessage) ([]byte, error) {
	return e.RenderTemplate(ctx, content, nil, values)
}

func TestProfileGenerationGuardsAndPersistentReport(t *testing.T) {
	for _, mode := range []string{"success", "blocking", "revoked_during_render"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			actor := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
			storage, err := NewLocalDocumentStorage(t.TempDir(), domain.MaxGeneratedBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			source, err := storage.Save(ctx, bytes.NewReader(docxBytes(t, "")), "source.docx")
			if err != nil {
				t.Fatal(err)
			}
			profile := json.RawMessage(`{"profile_version":2,"profile_id":"test","revision":"2.0.1","engine_version":"docx-profile/2.0.0"}`)
			repo := &templateRepoFake{template: domain.DocumentTemplate{ID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(), Enabled: true, Profile: profile, SchemaVersion: 2}}
			repo.draft = domain.TemplateDraft{ID: uuid.New(), OwnerID: actor.ID, TemplateID: repo.template.ID, DocumentVersionID: repo.template.DocumentVersionID, Revision: 1, Profile: profile, Fields: []domain.TemplateField{{Key: "person", Type: "person", Required: true, MaxLength: 100}}, Values: map[string]json.RawMessage{"person": json.RawMessage(`"Тестов"`)}}
			access := &templateAccessFake{}
			engine := &profileEngineFake{templateEngineFake: templateEngineFake{output: docxBytes(t, "")}, validation: domain.TemplateValidation{Ready: true, ProfileVersion: 2, EngineVersion: "docx-profile/2.0.0", RequiredFields: []string{"person"}, Checks: []domain.TemplateCheck{{RuleID: "review", Category: "manual_review", Result: "review_required"}}}}
			if mode == "blocking" {
				engine.validation.Ready = false
				engine.validation.Checks = append(engine.validation.Checks, domain.TemplateCheck{RuleID: "date", Category: "blocking", Result: "failed"})
			}
			if mode == "revoked_during_render" {
				engine.after = func() { access.denied = true }
			}
			svc := NewTemplateService(repo, templateVersionFake{domain.DocumentVersion{ID: repo.template.DocumentVersionID, DocumentID: repo.template.DocumentID, StorageKey: source.Key, MIMEType: domain.DOCXMIME, SizeBytes: source.Size}}, access, storage, engine, zap.NewNop())
			g, err := svc.GenerateDocument(ctx, actor, repo.draft.ID, 1)
			switch mode {
			case "blocking":
				var report domain.TemplateValidation
				if !errors.As(err, &report) || engine.calls != 0 || report.Checks[len(report.Checks)-1].Result != "failed" {
					t.Fatal("blocking report lost", err)
				}
			case "revoked_during_render":
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("revoked ACL bypassed", err)
				}
			case "success":
				if err != nil {
					t.Fatal(err)
				}
				var snapshot struct {
					Values     map[string]string         `json:"values"`
					Validation domain.TemplateValidation `json:"validation"`
					Profile    json.RawMessage           `json:"profile"`
				}
				if json.Unmarshal(g.GenerationSnapshot, &snapshot) != nil || snapshot.Values["person"] != "Тестов" || snapshot.Validation.EngineVersion != "docx-profile/2.0.0" || len(snapshot.Validation.Checks) != 2 {
					t.Fatal("snapshot lost")
				}
				engine.err = domain.ErrTemplateEngine
				before := engine.inspections
				_, file, err := svc.DownloadGeneratedDocument(ctx, actor, g.ID)
				if err != nil {
					t.Fatal("download requires renderer", err)
				}
				file.Close()
				if engine.inspections != before {
					t.Fatal("download called renderer")
				}
			}
		})
	}
}
