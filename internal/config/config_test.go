package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := load(func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://localhost/assistant_test?sslmode=disable"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxOpenConns != 10 || cfg.Database.MaxIdleConns != 5 || cfg.Database.ConnectTimeout != 10*time.Second || cfg.MigrationTimeout != time.Minute || !cfg.AutoMigrate || cfg.AllowProductionDown {
		t.Fatalf("unexpected defaults (URL omitted): open=%d idle=%d connect=%s migrate=%s auto=%v down=%v",
			cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns, cfg.Database.ConnectTimeout, cfg.MigrationTimeout, cfg.AutoMigrate, cfg.AllowProductionDown)
	}
	if cfg.Logger.Environment != "development" || cfg.Logger.Level != "info" || cfg.Logger.ServiceName != "ai-assistant" {
		t.Errorf("unexpected logger defaults: %+v", cfg.Logger)
	}
}

func TestOverrides(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://localhost/assistant_test", "APP_ENV": "production",
		"LOG_LEVEL": "debug", "SERVICE_NAME": "test-service", "DB_MAX_OPEN_CONNS": "20",
		"DB_MAX_IDLE_CONNS": "0", "DB_CONNECT_TIMEOUT": "2s", "DB_MIGRATION_TIMEOUT": "5m",
		"AUTO_MIGRATE": "false", "ALLOW_PRODUCTION_MIGRATION_DOWN": "true",
	}
	cfg, err := load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxOpenConns != 20 || cfg.Database.MaxIdleConns != 0 || cfg.Database.ConnectTimeout != 2*time.Second || cfg.MigrationTimeout != 5*time.Minute || cfg.AutoMigrate || !cfg.AllowProductionDown || cfg.Logger.Environment != "production" || cfg.Logger.Level != "debug" || cfg.Logger.ServiceName != "test-service" {
		t.Fatal("environment overrides were not applied")
	}
}

func TestInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_URL", ""}, {"DATABASE_URL", "not-a-postgres-url"},
		{"DB_MAX_OPEN_CONNS", "0"}, {"DB_MAX_OPEN_CONNS", "-1"}, {"DB_MAX_OPEN_CONNS", "ten"},
		{"DB_MAX_IDLE_CONNS", "-1"}, {"DB_MAX_IDLE_CONNS", "11"}, {"DB_MAX_IDLE_CONNS", "five"},
		{"DB_CONNECT_TIMEOUT", "0s"}, {"DB_CONNECT_TIMEOUT", "bad"},
		{"DB_MIGRATION_TIMEOUT", "-1s"}, {"DB_MIGRATION_TIMEOUT", "bad"},
		{"AUTO_MIGRATE", "perhaps"}, {"ALLOW_PRODUCTION_MIGRATION_DOWN", "perhaps"},
		{"APP_ENV", "prod"}, {"LOG_LEVEL", "trace"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			_, err := load(func(k string) string {
				if k == tc.key {
					return tc.value
				}
				if k == "DATABASE_URL" {
					return "postgres://localhost/assistant_test"
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected error identifying %s, got %v", tc.key, err)
			}
		})
	}
}

func TestInvalidURLDoesNotLeakSecrets(t *testing.T) {
	_, err := load(func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://app:secret-password@localhost/%wrong"
		}
		return ""
	})
	if err == nil || strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), "postgres://") && strings.Contains(err.Error(), "app:") {
		t.Fatalf("unsafe configuration error: %v", err)
	}
}

func TestHTTPConfiguration(t *testing.T) {
	base := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://localhost/assistant_test"
		}
		return ""
	}
	cfg, err := load(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateServer(); err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL != 12*time.Hour || !cfg.BootstrapAdmin || cfg.AdminUsername != "admin" || cfg.HTTPPort != 8080 || cfg.CookieSecure {
		t.Fatal("incorrect auth defaults")
	}
	for _, tc := range []struct{ key, value string }{
		{"SESSION_TTL", "0s"}, {"COOKIE_SECURE", "invalid"}, {"BOOTSTRAP_ADMIN", "invalid"},
		{"HTTP_PORT", "70000"}, {"ALLOWED_ORIGINS", "*"}, {"ALLOWED_ORIGINS", "http://localhost:8080/path"},
	} {
		cfg, err := load(func(k string) string {
			if k == tc.key {
				return tc.value
			}
			return base(k)
		})
		if err == nil {
			err = cfg.ValidateServer()
		}
		if err == nil {
			t.Errorf("invalid %s accepted", tc.key)
		}
	}
	production, err := load(func(k string) string {
		if k == "APP_ENV" {
			return "production"
		}
		return base(k)
	})
	if err != nil {
		t.Fatal("migration CLI must be able to load production config without TLS")
	}
	if err := production.ValidateServer(); err == nil {
		t.Fatal("insecure production server accepted")
	}
	production.AllowedOrigins = []string{"https://app.example"}
	production.TLSCertFile = "cert.pem"
	production.TLSKeyFile = "key.pem"
	if err := production.ValidateServer(); err != nil {
		t.Fatal(err)
	}
	production.CookieSecure = false
	if err := production.ValidateServer(); err == nil {
		t.Fatal("insecure production cookies accepted")
	}
}
