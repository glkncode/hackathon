package service

import (
	"context"
	"fmt"

	"ai-assistant/internal/domain"
)

type Users interface {
	Create(context.Context, domain.User) (domain.User, error)
	List(context.Context, int, int) ([]domain.User, int64, error)
}

type UserService struct{ users Users }

func NewUserService(users Users) *UserService { return &UserService{users: users} }

func requireAdmin(actor domain.User) error {
	if !actor.IsActive || actor.Role != domain.RoleAdmin {
		return domain.ErrAccessDenied
	}
	return nil
}

func validatePage(limit, offset int) error {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return domain.ErrInvalidInput
	}
	return nil
}

func (s *UserService) Create(ctx context.Context, actor domain.User, username, password string) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}
	if !validUsername(username) {
		return domain.User{}, domain.ErrInvalidInput
	}
	hash, err := HashPassword(password)
	if err != nil {
		return domain.User{}, err
	}
	user, err := s.users.Create(ctx, domain.User{Username: username, PasswordHash: hash, Role: domain.RoleMember, IsActive: true})
	user.PasswordHash = ""
	return user, err
}

func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (s *UserService) List(ctx context.Context, actor domain.User, limit, offset int) ([]domain.User, int64, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, 0, err
	}
	if err := validatePage(limit, offset); err != nil {
		return nil, 0, fmt.Errorf("pagination: %w", err)
	}
	users, total, err := s.users.List(ctx, limit, offset)
	for i := range users {
		users[i].PasswordHash = ""
	}
	return users, total, err
}
