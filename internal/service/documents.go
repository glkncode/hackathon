package service

import (
	"context"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Documents interface {
	DocumentReader
	CreateDocumentWithVersion(context.Context, domain.Document, domain.DocumentVersion) (domain.Document, domain.DocumentVersion, error)
	ListAll(context.Context, int, int) ([]domain.Document, int64, error)
	ListAccessibleByUser(context.Context, uuid.UUID, int, int) ([]domain.Document, int64, error)
	GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error)
	GetLatestVersion(context.Context, uuid.UUID) (domain.DocumentVersion, error)
	CreateVersion(context.Context, uuid.UUID, domain.DocumentVersion) (domain.DocumentVersion, error)
	ListVersions(context.Context, uuid.UUID, int, int) ([]domain.DocumentVersion, int64, error)
	GetActiveVersion(context.Context, uuid.UUID) (domain.DocumentVersion, error)
	SetActiveVersion(context.Context, uuid.UUID, uuid.UUID) error
	UpdateVersionStatus(context.Context, uuid.UUID, uuid.UUID, domain.VersionStatus) error
}
type DocumentAuthorization interface {
	Check(context.Context, domain.User, uuid.UUID) error
}
type DocumentDetails struct {
	domain.Document
	LatestVersion domain.DocumentVersion `json:"latest_version"`
	ActiveVersion domain.DocumentVersion `json:"active_version"`
}
type DocumentService struct {
	documents Documents
	access    DocumentAuthorization
	storage   DocumentStorage
	log       *zap.Logger
}

func NewDocumentService(documents Documents, access DocumentAuthorization, storage DocumentStorage, log *zap.Logger) *DocumentService {
	return &DocumentService{documents: documents, access: access, storage: storage, log: log}
}

func (s *DocumentService) Create(ctx context.Context, actor domain.User, title, filename string, reader io.Reader) (DocumentDetails, error) {
	if err := requireAdmin(actor); err != nil {
		return DocumentDetails{}, err
	}
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) < 1 || utf8.RuneCountInString(title) > 255 {
		return DocumentDetails{}, domain.ErrInvalidInput
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return DocumentDetails{}, domain.ErrInvalidInput
		}
	}
	file, err := s.storage.Save(ctx, reader, filename)
	if err != nil {
		return DocumentDetails{}, err
	}
	doc, version, err := s.documents.CreateDocumentWithVersion(ctx, domain.Document{Title: title, CreatedBy: actor.ID}, storedVersion(file))
	if err != nil {
		if cleanupErr := s.storage.Delete(file.Key); cleanupErr != nil {
			s.log.Error("document file cleanup failed", zap.Error(cleanupErr))
		}
		return DocumentDetails{}, err
	}
	s.log.Info("document_uploaded", zap.String("document_id", doc.ID.String()), zap.String("actor_id", actor.ID.String()), zap.Int64("size_bytes", version.SizeBytes))
	return DocumentDetails{Document: doc, LatestVersion: version, ActiveVersion: version}, nil
}
func (s *DocumentService) Get(ctx context.Context, actor domain.User, id uuid.UUID) (DocumentDetails, error) {
	if err := s.access.Check(ctx, actor, id); err != nil {
		return DocumentDetails{}, err
	}
	doc, err := s.documents.GetByID(ctx, id)
	if err != nil {
		return DocumentDetails{}, err
	}
	version, err := s.documents.GetLatestVersion(ctx, id)
	if err != nil {
		return DocumentDetails{}, err
	}
	if !doc.ActiveVersionID.Valid {
		return DocumentDetails{}, domain.ErrNotFound
	}
	// Resolve the pointer read with the document, so concurrent publication
	// cannot produce a mismatched active_version_id / active_version response.
	active, err := s.documents.GetVersionByID(ctx, doc.ActiveVersionID.UUID)
	if err != nil {
		return DocumentDetails{}, err
	}
	if active.DocumentID != id {
		return DocumentDetails{}, domain.ErrNotFound
	}
	return DocumentDetails{Document: doc, LatestVersion: version, ActiveVersion: active}, nil
}
func (s *DocumentService) List(ctx context.Context, actor domain.User, limit, offset int) ([]domain.Document, int64, error) {
	if !actor.IsActive || !actor.Role.Valid() {
		return nil, 0, domain.ErrAccessDenied
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, 0, err
	}
	if actor.Role == domain.RoleAdmin {
		return s.documents.ListAll(ctx, limit, offset)
	}
	return s.documents.ListAccessibleByUser(ctx, actor.ID, limit, offset)
}

// GetVersion checks the version's parent ACL; versions never carry separate ACL.
func (s *DocumentService) GetVersion(ctx context.Context, actor domain.User, documentID, id uuid.UUID) (domain.DocumentVersion, error) {
	if err := s.access.Check(ctx, actor, documentID); err != nil {
		return domain.DocumentVersion{}, err
	}
	version, err := s.documents.GetVersionByID(ctx, id)
	if err != nil {
		return domain.DocumentVersion{}, err
	}
	if version.DocumentID != documentID {
		return domain.DocumentVersion{}, domain.ErrNotFound
	}
	return version, nil
}
func (s *DocumentService) Download(ctx context.Context, actor domain.User, id uuid.UUID) (DocumentDetails, ReadSeekCloser, error) {
	details, err := s.Get(ctx, actor, id)
	if err != nil {
		return DocumentDetails{}, nil, err
	}
	file, err := s.storage.Open(details.ActiveVersion.StorageKey)
	return details, file, err
}

func storedVersion(file StoredFile) domain.DocumentVersion {
	return domain.DocumentVersion{StorageKey: file.Key, OriginalFilename: file.Filename, MIMEType: file.MIMEType, SizeBytes: file.Size, SHA256: file.SHA256}
}

func (s *DocumentService) UploadNewVersion(ctx context.Context, actor domain.User, documentID uuid.UUID, filename string, reader io.Reader) (domain.DocumentVersion, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.DocumentVersion{}, err
	}
	if _, err := s.documents.GetByID(ctx, documentID); err != nil {
		return domain.DocumentVersion{}, err
	}
	file, err := s.storage.Save(ctx, reader, filename)
	if err != nil {
		return domain.DocumentVersion{}, err
	}
	version, err := s.documents.CreateVersion(ctx, documentID, storedVersion(file))
	if err != nil {
		if cleanupErr := s.storage.Delete(file.Key); cleanupErr != nil {
			s.log.Error("document version file cleanup failed", zap.Error(cleanupErr))
		}
		return domain.DocumentVersion{}, err
	}
	s.log.Info("document_version_uploaded", zap.String("document_id", documentID.String()), zap.String("version_id", version.ID.String()), zap.Int("version_number", version.VersionNumber), zap.String("actor_id", actor.ID.String()))
	return version, nil
}

func (s *DocumentService) ListVersions(ctx context.Context, actor domain.User, documentID uuid.UUID, limit, offset int) ([]domain.DocumentVersion, int64, error) {
	if err := s.access.Check(ctx, actor, documentID); err != nil {
		return nil, 0, err
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, 0, err
	}
	if _, err := s.documents.GetByID(ctx, documentID); err != nil {
		return nil, 0, err
	}
	return s.documents.ListVersions(ctx, documentID, limit, offset)
}

func (s *DocumentService) DownloadVersion(ctx context.Context, actor domain.User, documentID, versionID uuid.UUID) (domain.DocumentVersion, ReadSeekCloser, error) {
	version, err := s.GetVersion(ctx, actor, documentID, versionID)
	if err != nil {
		return domain.DocumentVersion{}, nil, err
	}
	file, err := s.storage.Open(version.StorageKey)
	return version, file, err
}

func (s *DocumentService) GetActiveVersion(ctx context.Context, actor domain.User, documentID uuid.UUID) (domain.DocumentVersion, error) {
	if err := s.access.Check(ctx, actor, documentID); err != nil {
		return domain.DocumentVersion{}, err
	}
	return s.documents.GetActiveVersion(ctx, documentID)
}

// UpdateVersionStatus and PublishReadyVersion are trusted in-process integration
// operations. No public HTTP route exposes them. Ingestion publishes inside its
// fenced transaction so chunks, status and job completion commit together.
func (s *DocumentService) UpdateVersionStatus(ctx context.Context, documentID, versionID uuid.UUID, status domain.VersionStatus) error {
	if !status.Valid() {
		return domain.ErrInvalidInput
	}
	return s.documents.UpdateVersionStatus(ctx, documentID, versionID, status)
}

func (s *DocumentService) PublishReadyVersion(ctx context.Context, documentID, versionID uuid.UUID) error {
	if err := s.documents.SetActiveVersion(ctx, documentID, versionID); err != nil {
		return err
	}
	s.log.Info("document_version_published", zap.String("document_id", documentID.String()), zap.String("version_id", versionID.String()))
	return nil
}
