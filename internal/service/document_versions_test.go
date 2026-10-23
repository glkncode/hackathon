package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type versionStoreStub struct {
	documentStoreStub
	created                                           domain.DocumentVersion
	createVersionErr                                  error
	createCalls, listCalls, publishCalls, statusCalls int
	publishErr                                        error
}

func (f *versionStoreStub) CreateVersion(_ context.Context, id uuid.UUID, v domain.DocumentVersion) (domain.DocumentVersion, error) {
	f.createCalls++
	f.created = v
	v.ID = uuid.New()
	v.DocumentID = id
	v.VersionNumber = 2
	v.Status = domain.VersionUploaded
	return v, f.createVersionErr
}
func (f *versionStoreStub) ListVersions(context.Context, uuid.UUID, int, int) ([]domain.DocumentVersion, int64, error) {
	f.listCalls++
	return []domain.DocumentVersion{f.version}, 1, nil
}
func (f *versionStoreStub) SetActiveVersion(context.Context, uuid.UUID, uuid.UUID) error {
	f.publishCalls++
	return f.publishErr
}
func (f *versionStoreStub) UpdateVersionStatus(context.Context, uuid.UUID, uuid.UUID, domain.VersionStatus) error {
	f.statusCalls++
	return nil
}

func TestUploadVersionChecksumAndDatabaseFailureCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storage, err := NewLocalDocumentStorage(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	original, err := storage.Save(ctx, strings.NewReader("original"), "original.txt")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	docs := &versionStoreStub{documentStoreStub: documentStoreStub{doc: domain.Document{ID: id}, version: domain.DocumentVersion{ID: uuid.New(), DocumentID: id, StorageKey: original.Key}}}
	docs.doc.ActiveVersionID = uuid.NullUUID{UUID: docs.version.ID, Valid: true}
	acl := &accessStoreStub{allowed: true}
	s := NewDocumentService(docs, NewDocumentAccessService(acl, docs, nil, zap.NewNop()), storage, zap.NewNop())
	admin := domain.User{Role: domain.RoleAdmin, IsActive: true}
	member := domain.User{Role: domain.RoleMember, IsActive: true}
	if _, err := s.UploadNewVersion(ctx, member, id, "new.txt", strings.NewReader("new")); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member uploaded a version")
	}
	v, err := s.UploadNewVersion(ctx, admin, id, "../../new.txt", strings.NewReader("new original"))
	digest := sha256.Sum256([]byte("new original"))
	if err != nil || v.VersionNumber != 2 || v.Status != domain.VersionUploaded || v.SHA256 != hex.EncodeToString(digest[:]) || docs.created.StorageKey == original.Key || docs.created.OriginalFilename != "new.txt" {
		t.Fatalf("upload: %+v %v", v, err)
	}
	if docs.publishCalls != 0 || docs.doc.ActiveVersionID.UUID != docs.version.ID {
		t.Fatal("upload published an unprepared version")
	}
	old, err := storage.Open(original.Key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(old)
	old.Close()
	if err != nil || string(data) != "original" {
		t.Fatal("upload changed original")
	}
	before, _ := os.ReadDir(dir)
	docs.createVersionErr = errors.New("database unavailable")
	if _, err := s.UploadNewVersion(ctx, admin, id, "failed.txt", strings.NewReader("do not keep")); err == nil {
		t.Fatal("database failure ignored")
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatal("metadata failure left orphan file")
	}
	if _, err := s.UploadNewVersion(ctx, admin, id, "bad.pdf", strings.NewReader("not PDF")); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid format accepted")
	}
	if _, err := s.UploadNewVersion(ctx, admin, id, "huge.txt", strings.NewReader(strings.Repeat("x", 4097))); !errors.Is(err, ErrFileTooLarge) {
		t.Fatal("oversize accepted")
	}
}

func TestVersionReadsCheckParentAndACLBeforeOpening(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	versionID := uuid.New()
	docs := &versionStoreStub{documentStoreStub: documentStoreStub{doc: domain.Document{ID: id}, version: domain.DocumentVersion{ID: versionID, DocumentID: id}}}
	acl := &accessStoreStub{}
	// A nil storage intentionally catches any attempt to open an unauthorized file.
	s := NewDocumentService(docs, NewDocumentAccessService(acl, docs, nil, zap.NewNop()), nil, zap.NewNop())
	member := domain.User{Role: domain.RoleMember, IsActive: true}
	if _, _, err := s.ListVersions(ctx, member, id, 20, 0); !errors.Is(err, domain.ErrNotFound) || docs.listCalls != 0 {
		t.Fatal("history bypassed ACL")
	}
	if _, _, err := s.DownloadVersion(ctx, member, id, versionID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("download bypassed ACL")
	}
	if _, err := s.GetActiveVersion(ctx, member, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("active version bypassed ACL")
	}
	acl.allowed = true
	if _, _, err := s.ListVersions(ctx, member, id, 20, 0); err != nil || docs.listCalls != 1 {
		t.Fatal("allowed member cannot list history")
	}
	if _, _, err := s.DownloadVersion(ctx, member, uuid.New(), versionID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign document/version pair accepted")
	}
	member.IsActive = false
	if _, err := s.GetVersion(ctx, member, id, versionID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("inactive member accessed version")
	}
	if err := s.UpdateVersionStatus(ctx, id, versionID, "indexed"); !errors.Is(err, domain.ErrInvalidInput) || docs.statusCalls != 0 {
		t.Fatal("unknown status accepted")
	}
	docs.publishErr = domain.ErrInvalidInput
	if err := s.PublishReadyVersion(ctx, id, versionID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("publication failed open")
	}
}
