package main

import (
	"context"
	"testing"

	"ai-assistant/internal/config"
	"ai-assistant/internal/logger"
)

func TestCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""}, {[]string{"migrate", "up"}, "up"},
		{[]string{"migrate", "down"}, "down"}, {[]string{"migrate", "status"}, "status"},
		{[]string{"migrate", "version"}, "version"},
	} {
		got, err := command(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("command(%v) = %q, %v", tc.args, got, err)
		}
	}
	for _, args := range [][]string{{"down"}, {"migrate"}, {"migrate", "reset"}, {"migrate", "down", "extra"}} {
		if _, err := command(args); err == nil {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
}

func TestProductionRollbackPolicy(t *testing.T) {
	for _, env := range []string{"development", "production"} {
		for _, allowed := range []bool{false, true} {
			cfg := config.Config{Logger: logger.Config{Environment: env}, AllowProductionDown: allowed}
			for _, cmd := range []string{"", "up", "down", "status", "version"} {
				err := allowCommand(cmd, cfg)
				wantError := cmd == "down" && env == "production" && !allowed
				if (err != nil) != wantError {
					t.Errorf("env=%s allowed=%v command=%q: %v", env, allowed, cmd, err)
				}
			}
		}
	}
}

func TestRunInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if err := run(context.Background(), nil); err == nil {
		t.Fatal("missing database config must stop bootstrap")
	}
}

func TestProductionDownRejectedBeforeConnection(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://localhost/unreachable_test?sslmode=disable")
	t.Setenv("ALLOW_PRODUCTION_MIGRATION_DOWN", "false")
	err := run(context.Background(), []string{"migrate", "down"})
	if err == nil || err.Error() != "migrate down is disabled in production; explicit ALLOW_PRODUCTION_MIGRATION_DOWN=true is required" {
		t.Fatalf("rollback must be rejected before connecting: %v", err)
	}
}
