package postgres

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestIntegrationVersionNumberingAndPublication(t *testing.T) {
	db, ctx := integrationDB(t)
	creator := createTestUser(t, ctx, db, "version-owner")
	r := NewDocumentRepository(db)
	original := domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "v1.txt", MIMEType: "text/plain", SizeBytes: 10, SHA256: strings.Repeat("a", 64)}
	doc, v1, err := r.CreateDocumentWithVersion(ctx, domain.Document{Title: "Versioned", CreatedBy: creator.ID}, original)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Status != domain.VersionUploaded || !doc.ActiveVersionID.Valid || doc.ActiveVersionID.UUID != v1.ID {
		t.Fatal("initial file must be active but not indexed")
	}
	versions := []domain.DocumentVersion{v1}
	for _, n := range []int{2, 3} {
		input := original
		input.StorageKey = uuid.NewString()
		input.VersionNumber = 99
		input.Status = domain.VersionReady
		input.DocumentID = uuid.New()
		v, err := r.CreateVersion(ctx, doc.ID, input)
		if err != nil || v.VersionNumber != n || v.Status != domain.VersionUploaded || v.DocumentID != doc.ID {
			t.Fatalf("version %d: %+v %v", n, v, err)
		}
		versions = append(versions, v)
	}
	var wg sync.WaitGroup
	type result struct {
		v   domain.DocumentVersion
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		wg.Go(func() {
			input := original
			input.StorageKey = uuid.NewString()
			v, err := r.CreateVersion(ctx, doc.ID, input)
			results <- result{v, err}
		})
	}
	wg.Wait()
	close(results)
	numbers := map[int]bool{}
	for res := range results {
		if res.err != nil {
			t.Fatal(res.err)
		}
		numbers[res.v.VersionNumber] = true
		versions = append(versions, res.v)
	}
	if len(numbers) != 2 || !numbers[4] || !numbers[5] {
		t.Fatalf("concurrent numbering: %v", numbers)
	}
	history, total, err := r.ListVersions(ctx, doc.ID, 2, 1)
	if err != nil || total != 5 || len(history) != 2 || history[0].VersionNumber != 4 || history[1].VersionNumber != 3 {
		t.Fatal("history pagination/order failed")
	}
	if history, total, err := r.ListVersions(ctx, doc.ID, 20, 100); err != nil || total != 5 || len(history) != 0 {
		t.Fatal("empty history page lost total")
	}
	loaded, err := r.GetVersionByID(ctx, v1.ID)
	if err != nil || loaded != v1 {
		t.Fatal("creating new versions modified the original")
	}
	if active, err := r.GetActiveVersion(ctx, doc.ID); err != nil || active.ID != v1.ID {
		t.Fatal("upload changed active version")
	}
	if err := r.SetActiveVersion(ctx, doc.ID, versions[1].ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("published uploaded version")
	}
	if err := r.UpdateVersionStatus(ctx, doc.ID, versions[1].ID, domain.VersionProcessing); err != nil {
		t.Fatal(err)
	}
	if err := r.SetActiveVersion(ctx, doc.ID, versions[1].ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("published processing version")
	}
	if err := r.UpdateVersionStatus(ctx, doc.ID, versions[1].ID, domain.VersionFailed); err != nil {
		t.Fatal(err)
	}
	if err := r.SetActiveVersion(ctx, doc.ID, versions[1].ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("published failed version")
	}
	if err := r.UpdateVersionStatus(ctx, doc.ID, versions[1].ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if err := r.SetActiveVersion(ctx, doc.ID, versions[1].ID); err != nil {
		t.Fatal(err)
	}
	if active, err := r.GetActiveVersion(ctx, doc.ID); err != nil || active.ID != versions[1].ID || active.Status != domain.VersionReady {
		t.Fatal("ready publication failed")
	}
	foreign, foreignVersion, err := r.CreateDocumentWithVersion(ctx, domain.Document{Title: "Other", CreatedBy: creator.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "other.txt", MIMEType: "text/plain", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateVersionStatus(ctx, foreign.ID, foreignVersion.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if err := r.SetActiveVersion(ctx, doc.ID, foreignVersion.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("published another document's version")
	}
	if err := r.UpdateVersionStatus(ctx, doc.ID, foreignVersion.ID, domain.VersionFailed); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("changed another document's status")
	}
	if _, err := db.ExecContext(ctx, `UPDATE documents SET active_version_id=$2 WHERE id=$1`, doc.ID, foreignVersion.ID); !errors.Is(mapError("test foreign active", err), domain.ErrInvalidInput) {
		t.Fatal("database permits a cross-document active version")
	}
	if err := r.UpdateVersionStatus(ctx, doc.ID, v1.ID, "indexed"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid status accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE document_versions SET status='indexed' WHERE id=$1`, v1.ID); !errors.Is(mapError("test invalid status", err), domain.ErrInvalidInput) {
		t.Fatal("database status constraint missing")
	}
	if _, err := db.ExecContext(ctx, `UPDATE document_versions SET sha256='invalid' WHERE id=$1`, v1.ID); !errors.Is(mapError("test invalid checksum", err), domain.ErrInvalidInput) {
		t.Fatal("database checksum constraint missing")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	restarted := NewDocumentRepository(db)
	if history, total, err := restarted.ListVersions(ctx, doc.ID, 20, 0); err != nil || total != 5 || len(history) != 5 {
		t.Fatal("restart lost history")
	}
	if active, err := restarted.GetActiveVersion(ctx, doc.ID); err != nil || active.ID != versions[1].ID {
		t.Fatal("restart changed published version")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id=4 AND is_applied`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration 4 reapplied")
	}
}

func TestIntegrationVersioningUpgradePreservesLegacyDocuments(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "legacy-owner")
	session := createTestSession(t, ctx, db, user.ID, "legacy-session")
	rollbackTo(t, ctx, db, 3)
	docID, versionID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO documents (id,title,created_by) VALUES ($1,'Existing original',$2)`, docID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO document_versions (id,document_id,version_number,storage_key,original_filename,mime_type,size_bytes) VALUES ($1,$2,1,$3,'old.txt','text/plain',42)`, versionID, docID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	r := NewDocumentRepository(db)
	doc, err := r.GetByID(ctx, docID)
	if err != nil || !doc.ActiveVersionID.Valid || doc.ActiveVersionID.UUID != versionID {
		t.Fatal("legacy active pointer not backfilled")
	}
	active, err := r.GetActiveVersion(ctx, docID)
	if err != nil || active.ID != versionID || active.Status != domain.VersionUploaded || active.SHA256 != "" || !active.UpdatedAt.Equal(active.CreatedAt) {
		t.Fatalf("legacy metadata: %+v %v", active, err)
	}
	if persisted, err := NewSessionRepository(db).GetByTokenHash(ctx, session.TokenHash); err != nil || persisted.ID != session.ID || persisted.RevokedAt != nil {
		t.Fatal("upgrade changed sessions")
	}
	if persisted, err := NewUserRepository(db).GetByID(ctx, user.ID); err != nil || persisted != user {
		t.Fatal("upgrade changed user")
	}
	rollbackTo(t, ctx, db, 3)
	var storedID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM document_versions WHERE document_id=$1 AND version_number=1`, docID).Scan(&storedID); err != nil || storedID != versionID {
		t.Fatal("versioning Down deleted original metadata")
	}
}
