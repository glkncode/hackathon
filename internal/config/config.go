// Package config loads application settings from environment; no .env is required.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ai-assistant/internal/database"
	"ai-assistant/internal/logger"
)

type Config struct {
	Logger              logger.Config
	Database            database.Config
	MigrationTimeout    time.Duration
	AutoMigrate         bool
	AllowProductionDown bool
	HTTPPort            int
	TLSCertFile         string
	TLSKeyFile          string
	BootstrapAdmin      bool
	AdminUsername       string
	AdminPassword       string // Secret: never log Config.
	SessionTTL          time.Duration
	CookieSecure        bool
	AllowedOrigins      []string
}

func Load() (Config, error) { return load(os.Getenv) }

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Logger: logger.Config{
			Environment: value(getenv, "APP_ENV", "development"),
			Level:       value(getenv, "LOG_LEVEL", "info"),
			ServiceName: value(getenv, "SERVICE_NAME", "ai-assistant"),
		},
		Database: database.Config{
			URL: getenv("DATABASE_URL"), MaxOpenConns: 10, MaxIdleConns: 5,
			ConnectTimeout: 10 * time.Second,
		},
		MigrationTimeout: time.Minute,
		AutoMigrate:      true,
		HTTPPort:         8080,
		BootstrapAdmin:   true,
		AdminUsername:    value(getenv, "ADMIN_USERNAME", "admin"),
		AdminPassword:    getenv("ADMIN_PASSWORD"),
		SessionTTL:       12 * time.Hour,
		TLSCertFile:      getenv("HTTP_TLS_CERT_FILE"),
		TLSKeyFile:       getenv("HTTP_TLS_KEY_FILE"),
	}
	cfg.CookieSecure = cfg.Logger.Environment == "production"
	origins := getenv("ALLOWED_ORIGINS")
	if origins == "" && cfg.Logger.Environment == "development" {
		origins = "http://localhost:5173,http://localhost:8080"
	}
	if origins != "" {
		for _, origin := range strings.Split(origins, ",") {
			cfg.AllowedOrigins = append(cfg.AllowedOrigins, strings.TrimSpace(origin))
		}
	}
	for _, setting := range []struct {
		key string
		dst *int
	}{
		{"DB_MAX_OPEN_CONNS", &cfg.Database.MaxOpenConns},
		{"DB_MAX_IDLE_CONNS", &cfg.Database.MaxIdleConns},
		{"HTTP_PORT", &cfg.HTTPPort},
	} {
		if raw := getenv(setting.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return Config{}, fmt.Errorf("config: %s must be an integer", setting.key)
			}
			*setting.dst = n
		}
	}
	for _, setting := range []struct {
		key string
		dst *time.Duration
	}{
		{"DB_CONNECT_TIMEOUT", &cfg.Database.ConnectTimeout},
		{"DB_MIGRATION_TIMEOUT", &cfg.MigrationTimeout},
		{"SESSION_TTL", &cfg.SessionTTL},
	} {
		if raw := getenv(setting.key); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil || d <= 0 {
				return Config{}, fmt.Errorf("config: %s must be a positive duration (for example 10s)", setting.key)
			}
			*setting.dst = d
		}
	}
	for _, setting := range []struct {
		key string
		dst *bool
	}{
		{"AUTO_MIGRATE", &cfg.AutoMigrate},
		{"ALLOW_PRODUCTION_MIGRATION_DOWN", &cfg.AllowProductionDown},
		{"BOOTSTRAP_ADMIN", &cfg.BootstrapAdmin},
		{"COOKIE_SECURE", &cfg.CookieSecure},
	} {
		if raw := getenv(setting.key); raw != "" {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return Config{}, fmt.Errorf("config: %s must be a boolean", setting.key)
			}
			*setting.dst = b
		}
	}
	if cfg.Logger.Environment != "development" && cfg.Logger.Environment != "production" {
		return Config{}, errors.New("config: APP_ENV must be development or production")
	}
	switch cfg.Logger.Level {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, errors.New("config: LOG_LEVEL must be debug, info, warn, or error")
	}
	if err := cfg.Database.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ValidateServer is intentionally separate: migration CLI commands do not need
// an administrator password, origins, or TLS certificates.
func (c Config) ValidateServer() error {
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return errors.New("config: HTTP_PORT must be between 1 and 65535")
	}
	if c.SessionTTL < time.Second {
		return errors.New("config: SESSION_TTL must be at least one second")
	}
	if len(c.AllowedOrigins) == 0 {
		return errors.New("config: ALLOWED_ORIGINS must explicitly list trusted origins")
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || origin != u.Scheme+"://"+u.Host {
			return errors.New("config: ALLOWED_ORIGINS must contain exact http(s) origins without paths or wildcards")
		}
		if strings.Contains(u.Host, "*") || (c.Logger.Environment == "production" && u.Scheme != "https") {
			return errors.New("config: production origins must use HTTPS; wildcards are forbidden")
		}
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("config: HTTP_TLS_CERT_FILE and HTTP_TLS_KEY_FILE must be configured together")
	}
	if c.Logger.Environment == "production" && (!c.CookieSecure || c.TLSCertFile == "") {
		return errors.New("config: production requires COOKIE_SECURE=true and HTTP_TLS_CERT_FILE/HTTP_TLS_KEY_FILE for HTTPS")
	}
	if c.CookieSecure && c.TLSCertFile == "" {
		return errors.New("config: secure cookies require configured HTTPS certificates")
	}
	return nil
}

func value(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}
