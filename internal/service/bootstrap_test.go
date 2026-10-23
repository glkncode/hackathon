package service

import (
	"context"
	"sync"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestBootstrapAdminLifecycle(t *testing.T) {
	users := &fakeUsers{}
	cfg := BootstrapConfig{Enabled: true, Username: "admin", Password: "initial-password-123"}
	if err := BootstrapAdmin(context.Background(), users, zap.NewNop(), cfg); err != nil {
		t.Fatal(err)
	}
	if users.inserts != 1 || users.user.Role != domain.RoleAdmin || !users.user.IsActive {
		t.Fatal("initial admin not created")
	}
	if ok, err := VerifyPassword(cfg.Password, users.user.PasswordHash); err != nil || !ok {
		t.Fatal("bootstrap password was not hashed correctly")
	}
	before := users.user
	cfg.Password = ""
	if err := BootstrapAdmin(context.Background(), users, zap.NewNop(), cfg); err != nil {
		t.Fatal("existing users must not require bootstrap password")
	}
	if users.user != before || users.inserts != 1 {
		t.Fatal("restart modified existing admin")
	}
}

func TestBootstrapDoesNotPromoteUsers(t *testing.T) {
	member := domain.User{ID: uuid.New(), Username: "admin", Role: domain.RoleMember, PasswordHash: "existing-hash"}
	users := &fakeUsers{user: member}
	if err := BootstrapAdmin(context.Background(), users, zap.NewNop(), BootstrapConfig{Enabled: true, Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	if users.user != member || users.inserts != 0 {
		t.Fatal("existing member was changed")
	}
}

func TestBootstrapPasswordRequiredOnlyWhenNeeded(t *testing.T) {
	for _, password := range []string{"", "too-short"} {
		users := &fakeUsers{}
		if err := BootstrapAdmin(context.Background(), users, zap.NewNop(), BootstrapConfig{Enabled: true, Username: "admin", Password: password}); err == nil || users.inserts != 0 {
			t.Fatal("invalid bootstrap password accepted")
		}
	}
	if err := BootstrapAdmin(context.Background(), nil, nil, BootstrapConfig{}); err != nil {
		t.Fatal("disabled bootstrap accessed its dependencies")
	}
}

func TestBootstrapConcurrentInstances(t *testing.T) {
	users := &fakeUsers{}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			results <- BootstrapAdmin(context.Background(), users, zap.NewNop(), BootstrapConfig{Enabled: true, Username: "admin", Password: "initial-password-123"})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if users.inserts != 1 {
		t.Fatal("concurrent bootstrap created more than one account")
	}
}
