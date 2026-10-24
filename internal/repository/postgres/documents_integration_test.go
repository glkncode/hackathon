package postgres

import (
	"errors"
	"sync"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestIntegrationDocumentRepositoriesAndMigration(t *testing.T) {
	db, ctx := integrationDB(t)
	creator := createTestUser(t, ctx, db, "creator")
	member := createTestUser(t, ctx, db, "member")
	session := createTestSession(t, ctx, db, member.ID, "preserved")
	docs := NewDocumentRepository(db)
	acl := NewDocumentAccessRepository(db)
	doc, version, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Policy", CreatedBy: creator.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "policy.txt", MIMEType: "text/plain", SizeBytes: 42})
	if err != nil || doc.ID == uuid.Nil || version.DocumentID != doc.ID || version.VersionNumber != 1 {
		t.Fatalf("create: %+v %+v %v", doc, version, err)
	}
	loaded, err := docs.GetByID(ctx, doc.ID)
	if err != nil || loaded != doc {
		t.Fatal("document did not round-trip")
	}
	latest, err := docs.GetLatestVersion(ctx, doc.ID)
	if err != nil || latest != version {
		t.Fatal("latest version did not round-trip")
	}
	byID, err := docs.GetVersionByID(ctx, version.ID)
	if err != nil || byID != version {
		t.Fatal("version did not round-trip")
	}
	if _, err := docs.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing document not mapped")
	}
	if _, err := docs.GetVersionByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing version not mapped")
	}
	if items, total, err := docs.ListAccessibleByUser(ctx, member.ID, 20, 0); err != nil || len(items) != 0 || total != 0 {
		t.Fatal("ACL defaults to public")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := acl.GrantAccess(ctx, domain.DocumentAccess{DocumentID: doc.ID, UserID: member.ID, GrantedBy: creator.ID})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	succeeded, duplicates := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, domain.ErrAlreadyExists) {
			duplicates++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || duplicates != 7 {
		t.Fatalf("concurrent grants: success=%d duplicates=%d", succeeded, duplicates)
	}
	if allowed, err := acl.HasAccess(ctx, doc.ID, member.ID); err != nil || !allowed {
		t.Fatal("grant not effective")
	}
	if items, total, err := docs.ListAccessibleByUser(ctx, member.ID, 20, 0); err != nil || total != 1 || len(items) != 1 || items[0].ID != doc.ID {
		t.Fatal("filtered list failed")
	}
	if items, total, err := docs.ListAccessibleByUser(ctx, member.ID, 20, 50); err != nil || total != 1 || len(items) != 0 {
		t.Fatal("empty page lost total")
	}
	if items, total, err := acl.ListDocumentUsers(ctx, doc.ID, 20, 0); err != nil || total != 1 || len(items) != 1 || items[0].PasswordHash != "" {
		t.Fatal("ACL user list failed")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO document_versions (document_id,version_number,storage_key,original_filename,mime_type,size_bytes) VALUES ($1,1,'duplicate','x.txt','text/plain',1)`, doc.ID); !errors.Is(mapError("test", err), domain.ErrAlreadyExists) {
		t.Fatal("version uniqueness missing")
	}
	// A later version retains the document ACL without a separate access row.
	if _, err := db.ExecContext(ctx, `INSERT INTO document_versions (document_id,version_number,storage_key,original_filename,mime_type,size_bytes) VALUES ($1,2,$2,'v2.txt','text/plain',2)`, doc.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if latest, err := docs.GetLatestVersion(ctx, doc.ID); err != nil || latest.VersionNumber != 2 {
		t.Fatal("latest version is not ordered")
	}
	// Verify each foreign key, including grantor and version parent.
	invalid := uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO documents (title,created_by) VALUES ('bad',$1)`, []any{invalid}},
		{`INSERT INTO document_versions (document_id,version_number,storage_key,original_filename,mime_type,size_bytes) VALUES ($1,1,'bad','bad.txt','text/plain',1)`, []any{invalid}},
		{`INSERT INTO document_access (document_id,user_id,granted_by) VALUES ($1,$2,$3)`, []any{invalid, member.ID, creator.ID}},
		{`INSERT INTO document_access (document_id,user_id,granted_by) VALUES ($1,$2,$3)`, []any{doc.ID, invalid, creator.ID}},
		{`INSERT INTO document_access (document_id,user_id,granted_by) VALUES ($1,$2,$3)`, []any{doc.ID, creator.ID, invalid}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); !errors.Is(mapError("test FK", err), domain.ErrInvalidInput) {
			t.Fatalf("foreign key not enforced: %v", err)
		}
	}
	_, _, err = docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Must roll back", CreatedBy: creator.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "invalid.txt", MIMEType: "text/plain", SizeBytes: -1})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid version accepted")
	}
	if items, total, err := docs.ListAll(ctx, 20, 0); err != nil || total != 1 || len(items) != 1 {
		t.Fatal("failed version left document behind")
	}
	for range 2 {
		if err := acl.RevokeAccess(ctx, doc.ID, member.ID); err != nil {
			t.Fatal(err)
		}
	}
	if allowed, err := acl.HasAccess(ctx, doc.ID, member.ID); err != nil || allowed {
		t.Fatal("revoke failed")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	// Migration 3 Down removes only its own tables; user/session history survives.
	// Roll back versioning metadata before rolling back the original tables.
	rollbackTo(t, ctx, db, 2)
	if v, err := migrator.Version(ctx, db); err != nil || v != 2 {
		t.Fatal("unexpected schema version after documents Down")
	}
	if u, err := NewUserRepository(db).GetByID(ctx, member.ID); err != nil || u != member {
		t.Fatal("documents migration changed user")
	}
	if s, err := NewSessionRepository(db).GetByTokenHash(ctx, session.TokenHash); err != nil || s.ID != session.ID || s.RevokedAt != nil {
		t.Fatal("documents migration changed session")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if v, err := migrator.Version(ctx, db); err != nil || v != 11 {
		t.Fatal("documents migration not reapplied")
	}
}
