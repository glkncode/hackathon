package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

type InitialUsers interface {
	HasUsers(context.Context) (bool, error)
	// CreateIfEmpty must atomically recheck emptiness and insert, serializing
	// concurrent bootstraps. It never modifies any existing user.
	CreateIfEmpty(context.Context, domain.User) (bool, error)
}

type BootstrapConfig struct {
	Enabled  bool
	Username string
	Password string
}

func BootstrapAdmin(ctx context.Context, users InitialUsers, log *zap.Logger, cfg BootstrapConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if users == nil || log == nil {
		return errors.New("bootstrap: repository and logger are required")
	}
	exists, err := users.HasUsers(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	} // Existing users also permit removing the bootstrap secret.
	username := strings.TrimSpace(cfg.Username)
	if username == "" || utf8.RuneCountInString(username) > 64 {
		return errors.New("bootstrap: ADMIN_USERNAME must contain 1 to 64 characters")
	}
	hash, err := HashPassword(cfg.Password)
	if err != nil {
		return errors.New("bootstrap: valid ADMIN_PASSWORD is required for an empty users table (12 characters minimum, 1024 bytes maximum)")
	}
	created, err := users.CreateIfEmpty(ctx, domain.User{Username: username, PasswordHash: hash, Role: domain.RoleAdmin, IsActive: true})
	if err != nil {
		return err
	}
	if created {
		log.Info("initial administrator created")
	}
	return nil
}
