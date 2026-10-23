package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-assistant/internal/database"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func testDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integration test: set TEST_DATABASE_URL to a separate disposable PostgreSQL database ending in _test")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL (value omitted)")
	}
	if !strings.HasSuffix(parsed.Database, "_test") {
		t.Fatal("refusing integration test: database name must end in _test")
	}
	cfg := database.Config{URL: dsn, MaxOpenConns: 8, MaxIdleConns: 4, ConnectTimeout: 5 * time.Second}
	admin, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	schema := "auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("test database must be a PostgreSQL URL")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	cfg.URL = u.String()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func TestIntegrationHTTPAuthentication(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "HTTP"
		if secure {
			name = "HTTPS"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx := testDatabase(t)
			users := postgres.NewUserRepository(db)
			sessions := postgres.NewSessionRepository(db)
			password := "integration-password-123"
			if err := service.BootstrapAdmin(ctx, users, zap.NewNop(), service.BootstrapConfig{Enabled: true, Username: "admin", Password: password}); err != nil {
				t.Fatal(err)
			}
			auth, err := service.NewAuthService(users, sessions, zap.NewNop(), service.AuthConfig{})
			if err != nil {
				t.Fatal(err)
			}
			origin := "http://app.test"
			if secure {
				origin = "https://app.test"
			}
			router, err := httptransport.NewRouter(auth, zap.NewNop(), httptransport.RouterConfig{Production: secure, CookieSecure: secure, AllowedOrigins: []string{origin}})
			if err != nil {
				t.Fatal(err)
			}
			var server *httptest.Server
			if secure {
				server = httptest.NewTLSServer(router)
			} else {
				server = httptest.NewServer(router)
			}
			t.Cleanup(server.Close)
			client := server.Client()
			jar, _ := cookiejar.New(nil)
			client.Jar = jar
			client.Timeout = 5 * time.Second
			request := func(method, path, body, csrf string) (*http.Response, string) {
				t.Helper()
				req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", origin)
				req.Header.Set("Content-Type", "application/json")
				if csrf != "" {
					req.Header.Set("X-CSRF-Token", csrf)
				}
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				return res, string(data)
			}
			if res, _ := request("GET", "/api/v1/auth/me", "", ""); res.StatusCode != 401 {
				t.Fatal("anonymous me accepted")
			}
			var previous string
			for _, payload := range []string{`{"username":"unknown","password":"wrong-password"}`, `{"username":"admin","password":"wrong-password"}`} {
				res, body := request("POST", "/api/v1/auth/login", payload, "")
				if res.StatusCode != 401 {
					t.Fatal("invalid credentials accepted")
				}
				if previous != "" && previous != body {
					t.Fatal("credential response enumerates users")
				}
				previous = body
			}
			res, body := request("POST", "/api/v1/auth/login", `{"username":"ADMIN","password":"`+password+`"}`, "")
			if res.StatusCode != 200 {
				t.Fatalf("login failed: %d %s", res.StatusCode, body)
			}
			var login struct {
				User struct {
					ID uuid.UUID `json:"id"`
				}
			}
			if err := json.Unmarshal([]byte(body), &login); err != nil {
				t.Fatal(err)
			}
			var token, csrf string
			for _, cookie := range res.Cookies() {
				if cookie.Domain != "" || cookie.Path != "/" || cookie.Secure != secure {
					t.Fatal("incorrect cookie security")
				}
				if strings.HasSuffix(cookie.Name, "ai_session") {
					token = cookie.Value
					if !cookie.HttpOnly {
						t.Fatal("session cookie is not HttpOnly")
					}
				}
				if strings.HasSuffix(cookie.Name, "ai_csrf") {
					csrf = cookie.Value
					if cookie.HttpOnly {
						t.Fatal("CSRF cookie is not readable")
					}
				}
			}
			if token == "" || csrf == "" || strings.Contains(body, token) || strings.Contains(body, csrf) || strings.Contains(body, "password") {
				t.Fatal("login leaked secrets or omitted cookies")
			}
			hash, err := service.HashToken(token)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := sessions.GetByTokenHash(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := base64.RawURLEncoding.DecodeString(token)
			if bytes.Equal(stored.TokenHash, raw) || !bytes.Equal(stored.TokenHash, hash) {
				t.Fatal("raw session token persisted")
			}
			if res, body := request("GET", "/api/v1/auth/me", "", ""); res.StatusCode != 200 || strings.Contains(body, "password") || strings.Contains(body, "token") {
				t.Fatal("me failed or leaked secrets")
			}
			if res, _ := request("GET", "/api/v1/admin/ping", "", ""); res.StatusCode != 200 {
				t.Fatal("admin RBAC failed")
			}
			if _, err := db.ExecContext(ctx, `UPDATE users SET role='member' WHERE id=$1`, login.User.ID); err != nil {
				t.Fatal(err)
			}
			if res, _ := request("GET", "/api/v1/admin/ping", "", ""); res.StatusCode != 403 {
				t.Fatal("member passed admin RBAC")
			}
			for _, bad := range []string{"", "wrong"} {
				if res, _ := request("POST", "/api/v1/auth/logout", "", bad); res.StatusCode != 403 {
					t.Fatal("logout bypassed CSRF")
				}
			}
			if res, _ := request("POST", "/api/v1/auth/logout", "", csrf); res.StatusCode != 204 {
				t.Fatal("logout failed")
			}
			if res, _ := request("GET", "/api/v1/auth/me", "", ""); res.StatusCode != 401 {
				t.Fatal("cookie remained active after logout")
			}
			if _, err := auth.Authenticate(ctx, token); err != service.ErrUnauthorized {
				t.Fatal("old token remained valid")
			}
			if res, _ := request("POST", "/api/v1/auth/logout", "", ""); res.StatusCode != 204 {
				t.Fatal("repeat logout failed")
			}
		})
	}
}

func TestIntegrationConcurrentAdminBootstrap(t *testing.T) {
	db, ctx := testDatabase(t)
	users := postgres.NewUserRepository(db)
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for _, name := range []string{"admin-one", "admin-two", "admin-three", "admin-four"} {
		wg.Go(func() {
			results <- service.BootstrapAdmin(ctx, users, zap.NewNop(), service.BootstrapConfig{Enabled: true, Username: name, Password: "integration-password-123"})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bootstrap created %d users: %v", count, err)
	}
	var username string
	if err := db.QueryRowContext(ctx, `SELECT username FROM users`).Scan(&username); err != nil {
		t.Fatal(err)
	}
	before, err := users.GetByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if before.Role != domain.RoleAdmin || !before.IsActive {
		t.Fatal("initial account is not an active admin")
	}
	if ok, err := service.VerifyPassword("integration-password-123", before.PasswordHash); err != nil || !ok {
		t.Fatal("incorrect bootstrap password hash")
	}
	if err := service.BootstrapAdmin(ctx, users, zap.NewNop(), service.BootstrapConfig{Enabled: true, Username: "replacement", Password: ""}); err != nil {
		t.Fatal(err)
	}
	after, err := users.GetByID(ctx, before.ID)
	if err != nil || before != after {
		t.Fatal("restart changed admin")
	}
}
