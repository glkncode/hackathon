// Package database owns PostgreSQL connection pool creation, not application data.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Config is supplied by the application. Never log this struct or URL.
type Config struct {
	URL            string
	MaxOpenConns   int
	MaxIdleConns   int
	ConnectTimeout time.Duration
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.New("database: DATABASE_URL is required")
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("database: DATABASE_URL must be a valid postgres:// or postgresql:// URL")
	}
	if c.MaxOpenConns <= 0 {
		return errors.New("database: DB_MAX_OPEN_CONNS must be positive")
	}
	if c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		return errors.New("database: DB_MAX_IDLE_CONNS must be between zero and DB_MAX_OPEN_CONNS")
	}
	if c.ConnectTimeout <= 0 {
		return errors.New("database: DB_CONNECT_TIMEOUT must be positive")
	}
	return nil
}

// Open configures and pings the pool within ConnectTimeout. On failure it closes
// the pool; on success the caller must close it. It does not apply migrations.
func Open(ctx context.Context, cfg Config) (*sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pgcfg, err := pgx.ParseConfig(cfg.URL)
	if err != nil {
		return nil, &connectionError{operation: "parse DATABASE_URL (check connection options)", cause: err}
	}
	pgcfg.ConnectTimeout = cfg.ConnectTimeout
	db := stdlib.OpenDB(*pgcfg)
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		closeErr := db.Close()
		if pingCtx.Err() != nil {
			err = errors.Join(err, pingCtx.Err())
		}
		return nil, &connectionError{operation: "ping PostgreSQL (check availability and credentials)", cause: errors.Join(err, closeErr)}
	}
	return db, nil
}

// Driver parse/connection errors can contain a DSN or credentials. Keep the
// cause for errors.Is/As, but expose only safe operational context to logs.
type connectionError struct {
	operation string
	cause     error
}

func (e *connectionError) Error() string {
	message := fmt.Sprintf("database: %s failed", e.operation)
	if errors.Is(e.cause, context.DeadlineExceeded) {
		message += ": deadline exceeded"
	} else if errors.Is(e.cause, context.Canceled) {
		message += ": context canceled"
	}
	return message
}

func (e *connectionError) Unwrap() error { return e.cause }
