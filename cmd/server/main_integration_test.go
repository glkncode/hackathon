package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Exercise the actual entrypoint lifecycle with a cancellable context, including
// migrations, provisioning, a real listener, HTTP requests and graceful shutdown.
func TestIntegrationServerBootstrapAndShutdown(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integration test: TEST_DATABASE_URL must select a separate database ending in _test")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL (value omitted)")
	}
	if !strings.HasSuffix(parsed.Database, "_test") {
		t.Fatal("refusing test database without _test suffix")
	}
	admin, err := database.Open(context.Background(), database.Config{URL: dsn, MaxOpenConns: 2, MaxIdleConns: 1, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "main_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	setup, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSetup()
	if _, err := admin.ExecContext(setup, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("test DSN must be a URL")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close()
	origin := "http://localhost:" + strconv.Itoa(port)
	for key, value := range map[string]string{
		"DATABASE_URL": u.String(), "APP_ENV": "development", "HTTP_PORT": strconv.Itoa(port),
		"BOOTSTRAP_ADMIN": "true", "ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": "entrypoint-password-123",
		"AUTO_MIGRATE": "true", "COOKIE_SECURE": "false", "ALLOWED_ORIGINS": origin,
		"HTTP_TLS_CERT_FILE": "", "HTTP_TLS_KEY_FILE": "", "SESSION_TTL": "12h",
	} {
		t.Setenv(key, value)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(ctx, nil) }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if !stopped {
			select {
			case <-result:
			case <-time.After(12 * time.Second):
				t.Error("entrypoint did not stop")
			}
		}
	})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-result:
			stopped = true
			t.Fatalf("entrypoint exited before readiness: %v", err)
		default:
		}
		response, err := client.Get(origin + "/api/v1/auth/me")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 401 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	request := func(method, path, body, csrf string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	payload, _ := json.Marshal(map[string]string{"username": "admin", "password": "entrypoint-password-123"})
	login := request("POST", "/api/v1/auth/login", string(payload), "")
	if login.StatusCode != 200 {
		t.Fatal("login failed")
	}
	csrf := ""
	for _, cookie := range login.Cookies() {
		if cookie.Name == "ai_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("CSRF cookie missing")
	}
	if response := request("GET", "/api/v1/auth/me", "", ""); response.StatusCode != 200 {
		t.Fatal("me failed")
	}
	if response := request("POST", "/api/v1/auth/logout", "", ""); response.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if response := request("POST", "/api/v1/auth/logout", "", csrf); response.StatusCode != 204 {
		t.Fatal("logout failed")
	}
	if response := request("GET", "/api/v1/auth/me", "", ""); response.StatusCode != 401 {
		t.Fatal("session remained active")
	}
	cancel()
	select {
	case err := <-result:
		stopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("graceful shutdown timed out")
	}
	// Migration CLI remains usable without HTTP TLS settings or bootstrap secret.
	t.Setenv("APP_ENV", "production")
	t.Setenv("ADMIN_PASSWORD", "")
	if err := run(context.Background(), []string{"migrate", "status"}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"migrate", "version"}); err != nil {
		t.Fatal(err)
	}
}
