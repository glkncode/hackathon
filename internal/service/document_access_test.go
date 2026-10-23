package service

import (
	"context"
	"errors"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestDocumentAccessGrantPolicy(t *testing.T) {
	ctx := context.Background()
	admin := domain.User{ID: uuid.New(), Role: domain.RoleAdmin, IsActive: true}
	member := domain.User{ID: uuid.New(), Role: domain.RoleMember, IsActive: true}
	users := &fakeUsers{user: member}
	docs := &documentStoreStub{doc: domain.Document{ID: uuid.New()}}
	acl := &accessStoreStub{}
	s := NewDocumentAccessService(acl, docs, users, zap.NewNop())
	grant, err := s.Grant(ctx, admin, docs.doc.ID, member.ID)
	if err != nil || grant.GrantedBy != admin.ID || grant.DocumentID != docs.doc.ID || acl.granted != 1 {
		t.Fatal("grant failed")
	}
	if _, err := s.Grant(ctx, member, docs.doc.ID, member.ID); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member granted access")
	}
	if err := s.Revoke(ctx, member, docs.doc.ID, member.ID); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member revoked access")
	}
	if _, _, err := s.List(ctx, member, docs.doc.ID, 20, 0); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member listed ACL")
	}
	users.user.IsActive = false
	if _, err := s.Grant(ctx, admin, docs.doc.ID, member.ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("grant to inactive user accepted")
	}
	users.user = admin
	if _, err := s.Grant(ctx, admin, docs.doc.ID, admin.ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("grant to admin accepted")
	}
	for range 2 {
		if err := s.Revoke(ctx, admin, docs.doc.ID, member.ID); err != nil {
			t.Fatal(err)
		}
	}
	acl.err = errors.New("database unavailable")
	if err := s.Check(ctx, member, docs.doc.ID); err == nil {
		t.Fatal("ACL database error failed open")
	}
}
