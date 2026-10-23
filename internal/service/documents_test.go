package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type documentStoreStub struct {
	Documents
	doc                 domain.Document
	version             domain.DocumentVersion
	createErr           error
	listAll, listMember int
}

func (f *documentStoreStub) GetByID(context.Context, uuid.UUID) (domain.Document, error) {
	return f.doc, nil
}
func (f *documentStoreStub) CreateDocumentWithVersion(_ context.Context, d domain.Document, v domain.DocumentVersion) (domain.Document, domain.DocumentVersion, error) {
	return d, v, f.createErr
}
func (f *documentStoreStub) ListAll(context.Context, int, int) ([]domain.Document, int64, error) {
	f.listAll++
	return []domain.Document{f.doc}, 1, nil
}
func (f *documentStoreStub) ListAccessibleByUser(context.Context, uuid.UUID, int, int) ([]domain.Document, int64, error) {
	f.listMember++
	return []domain.Document{}, 0, nil
}
func (f *documentStoreStub) GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return f.version, nil
}
func (f *documentStoreStub) GetLatestVersion(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return f.version, nil
}
func (f *documentStoreStub) GetActiveVersion(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return f.version, nil
}

type accessStoreStub struct {
	allowed          bool
	err              error
	granted, revoked int
}

func (f *accessStoreStub) HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.allowed, f.err
}
func (f *accessStoreStub) GrantAccess(_ context.Context, a domain.DocumentAccess) (domain.DocumentAccess, error) {
	f.granted++
	return a, f.err
}
func (f *accessStoreStub) RevokeAccess(context.Context, uuid.UUID, uuid.UUID) error {
	f.revoked++
	return f.err
}
func (f *accessStoreStub) ListDocumentUsers(context.Context, uuid.UUID, int, int) ([]domain.User, int64, error) {
	return []domain.User{}, 0, f.err
}

func TestDocumentServiceAuthorizationAndFailureCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage, err := NewLocalDocumentStorage(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	docID := uuid.New()
	docs := &documentStoreStub{doc: domain.Document{ID: docID}, version: domain.DocumentVersion{ID: uuid.New(), DocumentID: docID}}
	docs.doc.ActiveVersionID = uuid.NullUUID{UUID: docs.version.ID, Valid: true}
	acl := &accessStoreStub{}
	access := NewDocumentAccessService(acl, docs, nil, zap.NewNop())
	s := NewDocumentService(docs, access, storage, zap.NewNop())
	admin := domain.User{ID: uuid.New(), Role: domain.RoleAdmin, IsActive: true}
	member := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	if _, err := s.Create(ctx, member, "Title", "file.txt", strings.NewReader("text")); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member uploaded file")
	}
	if _, err := s.Get(ctx, member, docID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("unauthorized metadata returned")
	}
	if _, err := s.GetVersion(ctx, member, docID, docs.version.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("version bypassed ACL")
	}
	if _, f, err := s.Download(ctx, member, docID); !errors.Is(err, domain.ErrNotFound) || f != nil {
		t.Fatal("download opened unauthorized file")
	}
	if items, total, err := s.List(ctx, member, 20, 0); err != nil || len(items) != 0 || total != 0 || docs.listMember != 1 || docs.listAll != 0 {
		t.Fatal("member list did not use filtered query")
	}
	if _, _, err := s.List(ctx, admin, 20, 0); err != nil || docs.listAll != 1 {
		t.Fatal("admin list failed")
	}
	acl.allowed = true
	if _, err := s.Get(ctx, member, docID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetVersion(ctx, member, docID, docs.version.ID); err != nil {
		t.Fatal(err)
	}
	member.IsActive = false
	if _, err := s.Get(ctx, member, docID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("inactive member read metadata")
	}
	docs.createErr = errors.New("database unavailable")
	if _, err := s.Create(ctx, admin, "Title", "file.txt", strings.NewReader("text")); err == nil {
		t.Fatal("DB failure hidden")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("DB failure left orphan file")
	}
}
