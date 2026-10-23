package database

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{URL: "postgres://app:secret-password@127.0.0.1/assistant_test?sslmode=disable", MaxOpenConns: 10, MaxIdleConns: 5, ConnectTimeout: time.Second}
}

func TestInvalidConfig(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"missing URL":      func(c *Config) { c.URL = "" },
		"wrong scheme":     func(c *Config) { c.URL = "mysql://localhost/db" },
		"invalid URL":      func(c *Config) { c.URL = "postgres://app:secret-password@localhost/%wrong" },
		"open connections": func(c *Config) { c.MaxOpenConns = 0 },
		"negative idle":    func(c *Config) { c.MaxIdleConns = -1 },
		"excessive idle":   func(c *Config) { c.MaxIdleConns = 11 },
		"timeout":          func(c *Config) { c.ConnectTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			mutate(&cfg)
			db, err := Open(context.Background(), cfg)
			if err == nil || db != nil {
				t.Fatal("invalid config must return nil pool and error")
			}
			if strings.Contains(err.Error(), "secret-password") {
				t.Fatal("error exposed credentials")
			}
		})
	}
}

func TestDriverParseErrorIsSafe(t *testing.T) {
	cfg := validConfig()
	cfg.URL += "&connect_timeout=not-a-number"
	db, err := Open(context.Background(), cfg)
	if db != nil || err == nil || strings.Contains(err.Error(), cfg.URL) || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("expected safe parse error, got %v", err)
	}
}

func TestUnavailablePostgreSQL(t *testing.T) {
	// A local TCP peer that closes immediately is not a PostgreSQL server.
	// This exercises a real pgx connection failure without an external service.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	cfg := validConfig()
	cfg.URL = "postgres://app:secret-password@" + listener.Addr().String() + "/assistant_test?sslmode=disable"
	db, err := Open(context.Background(), cfg)
	if db != nil || err == nil || !strings.Contains(err.Error(), "ping PostgreSQL") {
		t.Fatalf("expected connection error, got %v", err)
	}
	if strings.Contains(err.Error(), cfg.URL) || strings.Contains(err.Error(), "secret-password") {
		t.Fatal("connection error exposed credentials")
	}
}

func TestOpenContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", canceledContext(), context.Canceled},
		{"deadline", expiredContext(t), context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(tc.ctx, validConfig())
			if db != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestConnectionTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			// Accept TCP, but deliberately never answer the PostgreSQL handshake.
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	cfg := validConfig()
	cfg.ConnectTimeout = 50 * time.Millisecond
	cfg.URL = "postgres://app:secret-password@" + listener.Addr().String() + "/assistant_test?sslmode=disable"
	started := time.Now()
	db, err := Open(context.Background(), cfg)
	if db != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("connection timeout was not enforced: %v, elapsed=%s", err, time.Since(started))
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext(t *testing.T) context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	return ctx
}
