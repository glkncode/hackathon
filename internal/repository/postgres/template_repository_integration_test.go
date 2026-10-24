package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

func TestIntegrationTemplatePersistenceConcurrencyAndRollback(t *testing.T) {
	db, ctx := integrationDB(t)
	owner := createTestUser(t, ctx, db, "template_owner")
	other := createTestUser(t, ctx, db, "template_other")
	docs := NewDocumentRepository(db)
	doc, version, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Template", CreatedBy: owner.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "t.docx", MIMEType: domain.DOCXMIME, SizeBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	fields := []domain.TemplateField{{Key: "name", Label: "Имя", Question: "Кто?", Type: "person", Required: true, MaxLength: 100}}
	repo := NewTemplateRepository(db)
	template, err := repo.CreateTemplate(ctx, domain.DocumentTemplate{DocumentID: doc.ID, DocumentVersionID: version.ID, Name: "Template", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repo.CreateDraft(ctx, owner.ID, template)
	if err != nil || draft.Revision != 1 {
		t.Fatalf("draft: %v", err)
	}
	if _, err := repo.GetDraft(ctx, other.ID, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign draft exposed")
	}
	draft.Values = map[string]json.RawMessage{"name": json.RawMessage(`"Иванов"`)}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := repo.UpdateDraft(ctx, draft, 1)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, domain.ErrRevisionConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("CAS lost update")
	}
	loaded, err := repo.GetDraft(ctx, owner.ID, draft.ID)
	if err != nil || loaded.Status != "ready_to_generate" || loaded.Revision != 2 || loaded.DocumentVersionID != version.ID {
		t.Fatalf("round trip: %+v %v", loaded, err)
	}
	var calls atomic.Int32
	build := func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error) {
		calls.Add(1)
		return domain.GeneratedDocument{StorageKey: uuid.NewString(), Filename: "result.docx", MIMEType: domain.DOCXMIME, SizeBytes: 42, SHA256: strings.Repeat("a", 64)}, nil
	}
	results := make(chan domain.GeneratedDocument, 2)
	for range 2 {
		wg.Go(func() {
			g, err := repo.Generate(ctx, owner.ID, draft.ID, 2, build)
			if err != nil {
				t.Error(err)
			}
			results <- g
		})
	}
	wg.Wait()
	close(results)
	var first uuid.UUID
	for g := range results {
		if first == uuid.Nil {
			first = g.ID
		} else if first != g.ID {
			t.Fatal("duplicate generation")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate render")
	}
	if _, err := repo.GetGenerated(ctx, other.ID, first); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign result exposed")
	}
	if g, err := repo.GetGenerated(ctx, owner.ID, first); err != nil || g.SHA256 != strings.Repeat("a", 64) || g.DraftRevision != 2 {
		t.Fatal("metadata not persisted")
	}
	loaded, err = repo.UpdateDraft(ctx, loaded, 2)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("render failed")
	if _, err := repo.Generate(ctx, owner.ID, draft.ID, 3, func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error) {
		return domain.GeneratedDocument{}, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := repo.Generate(ctx, owner.ID, draft.ID, 3, func(context.Context, domain.TemplateDraft) (domain.GeneratedDocument, error) {
		return domain.GeneratedDocument{StorageKey: uuid.NewString()}, nil
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("constraint failure not propagated", err)
	}
	if g, err := repo.Generate(ctx, owner.ID, draft.ID, 3, build); err != nil || g.ID == first {
		t.Fatal("rollback/retry failed", err)
	}
	// Publishing a new source version must not move existing draft pointers.
	v2, err := docs.CreateVersion(ctx, doc.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "v2.docx", MIMEType: domain.DOCXMIME, SizeBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	if v2.ID == version.ID {
		t.Fatal("new version missing")
	}
	pinned, _ := repo.GetDraft(ctx, owner.ID, draft.ID)
	if pinned.DocumentVersionID != version.ID {
		t.Fatal("draft silently upgraded")
	}
}
