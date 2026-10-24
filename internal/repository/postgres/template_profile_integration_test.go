package postgres

import (
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
)

func TestIntegrationImmutableProfileAndValidationSnapshot(t *testing.T) {
	db, ctx := integrationDB(t)
	owner := createTestUser(t, ctx, db, "v2_owner")
	docs := NewDocumentRepository(db)
	doc, version, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Profile2", CreatedBy: owner.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "original.docx", MIMEType: domain.DOCXMIME, SizeBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../docs/template-profiles/v2/working-group.json")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := domain.ProfileFields(raw)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewTemplateRepository(db)
	template, err := repo.CreateTemplate(ctx, domain.DocumentTemplate{DocumentID: doc.ID, DocumentVersionID: version.ID, Name: "v2", Fields: fields, Profile: raw, SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repo.CreateDraft(ctx, owner.ID, template)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	_ = json.Unmarshal(raw, &before)
	_ = json.Unmarshal(draft.Profile, &after)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) || draft.SchemaVersion != 2 {
		t.Fatal("profile changed")
	}
	snapshot := json.RawMessage(`{"engine_version":"docx-profile/2.0.0","validation":{"ready":true,"checks":[{"rule_id":"manual","result":"review_required"}]}}`)
	generated, err := repo.Generate(ctx, owner.ID, draft.ID, 1, func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error) {
		return domain.GeneratedDocument{StorageKey: uuid.NewString(), Filename: "project.docx", MIMEType: domain.DOCXMIME, SizeBytes: 100, SHA256: strings.Repeat("a", 64), GenerationSnapshot: snapshot}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetGenerated(ctx, owner.ID, generated.ID)
	if err != nil || !strings.Contains(string(loaded.GenerationSnapshot), "review_required") {
		t.Fatal("report not persisted", err)
	}
	// New registrations never reinterpret the old draft; even enabled toggles keep its profile.
	_, err = repo.SetTemplateEnabled(ctx, template.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := repo.GetDraft(ctx, owner.ID, draft.ID)
	if err != nil || string(pinned.Profile) != string(draft.Profile) {
		t.Fatal("profile pin lost", err)
	}
	var mismatch int
	err = db.QueryRowContext(ctx, `INSERT INTO document_templates(document_id,document_version_id,name,fields,schema_version,profile) VALUES($1,$2,'invalid',$3,1,$4) RETURNING schema_version`, doc.ID, version.ID, mustJSON(fields), raw).Scan(&mismatch)
	if err == nil {
		t.Fatal("version mismatch accepted")
	}
	for _, profile := range []any{nil, []byte(`{}`), []byte(`{"profile_version":null}`)} {
		err = db.QueryRowContext(ctx, `INSERT INTO document_templates(document_id,document_version_id,name,fields,schema_version,profile) VALUES($1,$2,'invalid',$3,2,$4) RETURNING schema_version`, doc.ID, version.ID, mustJSON(fields), profile).Scan(&mismatch)
		if err == nil {
			t.Fatal("NULL profile integrity bypass")
		}
	}
}

func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
