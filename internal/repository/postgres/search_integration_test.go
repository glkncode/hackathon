package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type searchFixture struct {
	db                      *sql.DB
	ctx                     context.Context
	docs                    *DocumentRepository
	chunks                  *ChunkRepository
	acl                     *DocumentAccessRepository
	repo                    *SearchRepository
	admin, memberA, memberB domain.User
}

func newSearchFixture(t *testing.T) searchFixture {
	t.Helper()
	db, ctx := integrationDB(t)
	users := NewUserRepository(db)
	createUser := func(name string, role domain.Role) domain.User {
		t.Helper()
		u, err := users.Create(ctx, domain.User{Username: name, Role: role, PasswordHash: "test-hash", IsActive: true})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	return searchFixture{db: db, ctx: ctx, docs: NewDocumentRepository(db), chunks: NewChunkRepository(db),
		acl: NewDocumentAccessRepository(db), repo: NewSearchRepository(db),
		admin: createUser("admin", domain.RoleAdmin), memberA: createUser("member-a", domain.RoleMember), memberB: createUser("member-b", domain.RoleMember)}
}

func (f searchFixture) document(t *testing.T, title string, status domain.VersionStatus, chunks []domain.DocumentChunk) (domain.Document, domain.DocumentVersion) {
	t.Helper()
	d, v, err := f.docs.CreateDocumentWithVersion(f.ctx, domain.Document{Title: title, CreatedBy: f.admin.ID}, domain.DocumentVersion{
		StorageKey: uuid.NewString(), OriginalFilename: "rules.txt", MIMEType: "text/plain", SizeBytes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersion(t, v, status, chunks)
	return d, v
}

func (f searchFixture) seedVersion(t *testing.T, v domain.DocumentVersion, status domain.VersionStatus, chunks []domain.DocumentChunk) {
	t.Helper()
	if err := f.chunks.ReplaceVersionChunks(f.ctx, v.ID, chunks); err != nil {
		t.Fatal(err)
	}
	if err := f.docs.UpdateVersionStatus(f.ctx, v.DocumentID, v.ID, status); err != nil {
		t.Fatal(err)
	}
}

func (f searchFixture) find(t *testing.T, actor domain.User, query string, limit int) []domain.SearchResult {
	t.Helper()
	results, err := f.repo.Search(f.ctx, actor.ID, actor.Role, domain.SearchQuery{Query: query, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	if results == nil {
		t.Fatal("empty results must be an empty slice")
	}
	return results
}

func (f searchFixture) grant(t *testing.T, docID, userID uuid.UUID) {
	t.Helper()
	if _, err := f.acl.GrantAccess(f.ctx, domain.DocumentAccess{DocumentID: docID, UserID: userID, GrantedBy: f.admin.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSearchACLVersionsAndCitations(t *testing.T) {
	f := newSearchFixture(t)
	docA, old := f.document(t, "Инструкция A", domain.VersionReady, []domain.DocumentChunk{{Content: "архивныйкод согласование"}})
	newVersion, err := f.docs.CreateVersion(f.ctx, docA.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "v2.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	page, number, title := 12, "2.3.1", "Согласование документов"
	content := "Для согласования служебной записки требуется подпись руководителя."
	f.seedVersion(t, newVersion, domain.VersionReady, []domain.DocumentChunk{{ChunkIndex: 3, Content: content, PageNumber: &page, SectionNumber: &number, SectionTitle: &title}})
	if err := f.docs.SetActiveVersion(f.ctx, docA.ID, newVersion.ID); err != nil {
		t.Fatal(err)
	}
	docB, versionB := f.document(t, "Закрытая инструкция B", domain.VersionReady, []domain.DocumentChunk{{Content: "Закрытый порядок согласования документов"}})
	for _, status := range []domain.VersionStatus{domain.VersionUploaded, domain.VersionProcessing, domain.VersionFailed} {
		f.document(t, string(status), status, []domain.DocumentChunk{{Content: "согласование документов"}})
	}
	noActive, _ := f.document(t, "Без активной версии", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование документов"}})
	if _, err := f.db.ExecContext(f.ctx, `UPDATE documents SET active_version_id=NULL WHERE id=$1`, noActive.ID); err != nil {
		t.Fatal(err)
	}
	f.grant(t, docA.ID, f.memberA.ID)
	adminResults := f.find(t, f.admin, "согласование", 30)
	if len(adminResults) != 2 {
		t.Fatalf("admin must see only two active ready sources, got %d", len(adminResults))
	}
	for _, r := range adminResults {
		if r.DocumentID != docA.ID && r.DocumentID != docB.ID || r.DocumentVersionID == old.ID || r.Score <= 0 {
			t.Fatal("ineligible or historical result")
		}
	}
	results := f.find(t, f.memberA, "согласование", 10)
	if len(results) != 1 {
		t.Fatal("member A did not receive exactly its allowed source")
	}
	r := results[0]
	if r.DocumentID != docA.ID || r.DocumentVersionID != newVersion.ID || r.DocumentTitle != docA.Title || r.VersionNumber != 2 || r.ChunkIndex != 3 || r.Content != content || r.PageNumber == nil || *r.PageNumber != page || r.SectionNumber == nil || *r.SectionNumber != number || r.SectionTitle == nil || *r.SectionTitle != title {
		t.Fatal("citation does not match persisted version/chunk", r)
	}
	saved, err := f.chunks.GetChunksByVersion(f.ctx, newVersion.ID)
	if err != nil || len(saved) != 1 || saved[0].ID != r.ChunkID {
		t.Fatal("citation chunk ID was not persisted")
	}
	if len(f.find(t, f.memberB, "согласование", 10)) != 0 || len(f.find(t, f.admin, "архивныйкод", 10)) != 0 {
		t.Fatal("ACL or historical filtering failed")
	}
	f.grant(t, docB.ID, f.memberB.ID)
	results = f.find(t, f.memberB, "согласование", 10)
	if len(results) != 1 || results[0].DocumentVersionID != versionB.ID || results[0].PageNumber != nil || results[0].SectionNumber != nil || results[0].SectionTitle != nil {
		t.Fatal("grant or nullable citation metadata failed")
	}
	if err := f.acl.RevokeAccess(f.ctx, docB.ID, f.memberB.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.memberB, "согласование", 10)) != 0 {
		t.Fatal("revoked ACL remained visible")
	}
	for _, injection := range []string{"согласование' OR 1=1 --", "согласование OR закрытый", `'); SELECT * FROM document_chunks; --`, "' UNION SELECT NULL --"} {
		for _, r := range f.find(t, f.memberA, injection, 30) {
			if r.DocumentID != docA.ID {
				t.Fatal("injection bypassed authorization")
			}
		}
		if len(f.find(t, f.memberB, injection, 30)) != 0 {
			t.Fatal("injection revealed hidden documents")
		}
	}
	for _, query := range []string{"несуществующееслово", "и", "!!!", `"`, "-согласование"} {
		if len(f.find(t, f.admin, query, 10)) != 0 {
			t.Fatal("empty or stopword query unexpectedly matched", query)
		}
	}
	if err := NewUserRepository(f.db).SetActive(f.ctx, f.memberA.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.memberA, "согласование", 10)) != 0 {
		t.Fatal("stale identity bypassed account deactivation")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE users SET role='member' WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.admin, "согласование", 10)) != 0 {
		t.Fatal("stale admin identity bypassed role change")
	}
}

func TestIntegrationSearchRankingLimitsAndPublication(t *testing.T) {
	f := newSearchFixture(t)
	doc, version := f.document(t, "Ranking", domain.VersionReady, []domain.DocumentChunk{
		{ChunkIndex: 0, Content: "согласование"},
		{ChunkIndex: 1, Content: strings.Repeat("согласование ", 5)},
		{ChunkIndex: 2, Content: "согласование"},
	})
	results := f.find(t, f.admin, "согласования", 10)
	if len(results) != 3 || results[0].ChunkIndex != 1 || results[0].Score <= results[1].Score || results[1].ChunkIndex != 0 || results[2].ChunkIndex != 2 || results[1].Score != results[2].Score {
		t.Fatal("Russian stemming, rank or tie order incorrect", results)
	}
	if again := f.find(t, f.admin, "согласования", 10); !reflect.DeepEqual(again, results) {
		t.Fatal("results not deterministic")
	}
	if limited := f.find(t, f.admin, "согласования", 1); len(limited) != 1 || limited[0].ChunkID != results[0].ChunkID {
		t.Fatal("limit applied before relevance")
	}
	next, err := f.docs.CreateVersion(f.ctx, doc.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "next.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersion(t, next, domain.VersionReady, []domain.DocumentChunk{{Content: "новыйрегламент"}})
	if got := f.find(t, f.admin, "новыйрегламент", 10); len(got) != 0 {
		t.Fatal("unpublished ready version visible")
	}
	if err := f.docs.SetActiveVersion(f.ctx, doc.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.admin, "согласование", 10)) != 0 {
		t.Fatal("old active version remained searchable after publication")
	}
	if got := f.find(t, f.admin, "новыйрегламент", 10); len(got) != 1 || got[0].DocumentVersionID != next.ID || got[0].DocumentVersionID == version.ID {
		t.Fatal("new publication not searchable")
	}
	many := make([]domain.DocumentChunk, 35)
	for i := range many {
		many[i] = domain.DocumentChunk{ChunkIndex: i, Content: "массовыйпоиск"}
	}
	f.document(t, "Many", domain.VersionReady, many)
	if got := f.find(t, f.admin, "массовыйпоиск", 0); len(got) != 10 || got[0].ChunkIndex != 0 || got[9].ChunkIndex != 9 {
		t.Fatal("default limit or deterministic tie order incorrect")
	}
	if got := f.find(t, f.admin, "массовыйпоиск", 30); len(got) != 30 || got[29].ChunkIndex != 29 {
		t.Fatal("maximum limit not applied")
	}
}

func TestIntegrationSearchCancellationDuringDatabaseWait(t *testing.T) {
	f := newSearchFixture(t)
	f.document(t, "Locked", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование"}})
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(f.ctx, `LOCK TABLE document_chunks IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = f.repo.Search(ctx, f.admin.ID, f.admin.Role, domain.SearchQuery{Query: "согласование"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("search did not respect cancellation while waiting in PostgreSQL", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if results := f.find(t, f.admin, "согласование", 10); len(results) != 1 {
		t.Fatal("pool unusable after search cancellation")
	}
}
