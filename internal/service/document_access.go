package service

import (
	"context"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type DocumentAccessStore interface {
	GrantAccess(context.Context, domain.DocumentAccess) (domain.DocumentAccess, error)
	RevokeAccess(context.Context, uuid.UUID, uuid.UUID) error
	HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	ListDocumentUsers(context.Context, uuid.UUID, int, int) ([]domain.User, int64, error)
}
type DocumentReader interface {
	GetByID(context.Context, uuid.UUID) (domain.Document, error)
}
type UserReader interface {
	GetByID(context.Context, uuid.UUID) (domain.User, error)
}

type DocumentAccessService struct {
	access    DocumentAccessStore
	documents DocumentReader
	users     UserReader
	log       *zap.Logger
}

func NewDocumentAccessService(access DocumentAccessStore, documents DocumentReader, users UserReader, log *zap.Logger) *DocumentAccessService {
	return &DocumentAccessService{access: access, documents: documents, users: users, log: log}
}

// Check is the shared authorization gate for metadata, versions, downloads and
// future retrieval. A denial is indistinguishable from an unknown document.
func (s *DocumentAccessService) Check(ctx context.Context, actor domain.User, documentID uuid.UUID) error {
	if !actor.IsActive || !actor.Role.Valid() {
		return domain.ErrNotFound
	}
	if actor.Role == domain.RoleAdmin {
		return nil
	}
	allowed, err := s.access.HasAccess(ctx, documentID, actor.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return domain.ErrNotFound
	}
	return nil
}
func (s *DocumentAccessService) Grant(ctx context.Context, actor domain.User, documentID, userID uuid.UUID) (domain.DocumentAccess, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.DocumentAccess{}, err
	}
	if _, err := s.documents.GetByID(ctx, documentID); err != nil {
		return domain.DocumentAccess{}, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return domain.DocumentAccess{}, err
	}
	if !user.IsActive || user.Role != domain.RoleMember {
		return domain.DocumentAccess{}, domain.ErrInvalidInput
	}
	access, err := s.access.GrantAccess(ctx, domain.DocumentAccess{DocumentID: documentID, UserID: userID, GrantedBy: actor.ID})
	if err == nil {
		s.log.Info("document_access_granted", zap.String("document_id", documentID.String()), zap.String("user_id", userID.String()), zap.String("actor_id", actor.ID.String()))
	}
	return access, err
}
func (s *DocumentAccessService) Revoke(ctx context.Context, actor domain.User, documentID, userID uuid.UUID) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if _, err := s.documents.GetByID(ctx, documentID); err != nil {
		return err
	}
	if err := s.access.RevokeAccess(ctx, documentID, userID); err != nil {
		return err
	}
	s.log.Info("document_access_revoked", zap.String("document_id", documentID.String()), zap.String("user_id", userID.String()), zap.String("actor_id", actor.ID.String()))
	return nil
}
func (s *DocumentAccessService) List(ctx context.Context, actor domain.User, documentID uuid.UUID, limit, offset int) ([]domain.User, int64, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, 0, err
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, 0, err
	}
	if _, err := s.documents.GetByID(ctx, documentID); err != nil {
		return nil, 0, err
	}
	return s.access.ListDocumentUsers(ctx, documentID, limit, offset)
}
