package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type managedUsers struct{ fakeUsers }

func (f *managedUsers) Create(ctx context.Context, u domain.User) (domain.User, error) {
	if strings.EqualFold(u.Username, f.user.Username) {
		return domain.User{}, domain.ErrAlreadyExists
	}
	u.ID = uuid.New()
	f.user = u
	f.inserts++
	return u, nil
}
func (f *managedUsers) List(context.Context, int, int) ([]domain.User, int64, error) {
	return []domain.User{f.user}, 1, nil
}

func TestUserCreationAndExistingAuth(t *testing.T) {
	ctx := context.Background()
	users := &managedUsers{}
	s := NewUserService(users)
	admin := domain.User{ID: uuid.New(), Role: domain.RoleAdmin, IsActive: true}
	member, err := s.Create(ctx, admin, "Employee_1", "strong-password-123")
	if err != nil || member.Role != domain.RoleMember || !member.IsActive || member.PasswordHash != "" {
		t.Fatalf("create member: %+v %v", member, err)
	}
	if ok, err := VerifyPassword("strong-password-123", users.user.PasswordHash); err != nil || !ok {
		t.Fatal("password not hashed")
	}
	if _, err := s.Create(ctx, admin, "EMPLOYEE_1", "strong-password-123"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}
	auth, err := NewAuthService(users, &fakeSessions{items: make(map[string]domain.Session)}, zap.NewNop(), AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := auth.Login(ctx, "employee_1", "strong-password-123"); err != nil || result.User.ID != member.ID {
		t.Fatalf("new member cannot log in: %v", err)
	}
	if _, err := s.Create(ctx, member, "another", "strong-password-123"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member created a user")
	}
	if _, _, err := s.List(ctx, member, 20, 0); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("member listed users")
	}
	items, total, err := s.List(ctx, admin, 20, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].PasswordHash != "" {
		t.Fatal("list leaked hash")
	}
}
func TestUserInputValidation(t *testing.T) {
	users := &managedUsers{}
	s := NewUserService(users)
	admin := domain.User{Role: domain.RoleAdmin, IsActive: true}
	for _, name := range []string{"ab", "bad name", "кириллица", "a/b", "a\\b", strings.Repeat("a", 65)} {
		if _, err := s.Create(context.Background(), admin, name, "strong-password-123"); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("username %q: %v", name, err)
		}
	}
	for _, password := range []string{"short", strings.Repeat("a", MaxPasswordBytes+1)} {
		if _, err := s.Create(context.Background(), admin, "valid-name", password); !errors.Is(err, ErrInvalidPassword) {
			t.Fatal("invalid password accepted")
		}
	}
	if users.inserts != 0 {
		t.Fatal("invalid input reached repository")
	}
	for _, page := range [][2]int{{0, 0}, {101, 0}, {20, -1}, {20, 1_000_001}} {
		if _, _, err := s.List(context.Background(), admin, page[0], page[1]); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("invalid pagination accepted")
		}
	}
}
