package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/database"
	"ai-assistant/internal/domain"
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
		"RAG_ENABLED": "false", "RAG_TIMEOUT": "60s", "RAG_MAX_SOURCES": "8", "RAG_MAX_HISTORY_MESSAGES": "10",
		"AI_SERVICE_URL": "", "AI_SERVICE_TOKEN": "", "INGESTION_ENABLED": "true", "INGESTION_WORKERS": "1", "INGESTION_POLL_INTERVAL": "5s", "INGESTION_REQUEST_TIMEOUT": "120s", "INGESTION_MAX_ATTEMPTS": "5",
		"DOCUMENT_STORAGE_DIR": t.TempDir(), "DOCUMENT_MAX_UPLOAD_BYTES": "20971520",
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
	// Isolated schemas still share Goose's database-wide advisory lock. Its
	// default retry interval is 5s when other integration packages migrate.
	deadline := time.Now().Add(30 * time.Second)
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
	if response := request("POST", "/api/v1/search", `{"query":"согласование"}`, ""); response.StatusCode != 403 {
		t.Fatal("search accepted missing CSRF")
	}
	if response := request("POST", "/api/v1/search", `{"query":"согласование"}`, csrf); response.StatusCode != 200 {
		t.Fatal("entrypoint search unavailable without Python credentials")
	}
	// An actual entrypoint without AI credentials still accepts uploads and jobs.
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	if err := form.WriteField("title", "Queued without Python"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "queued.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "Internal document"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq, err := http.NewRequestWithContext(ctx, "POST", origin+"/api/v1/documents", &upload)
	if err != nil {
		t.Fatal(err)
	}
	uploadReq.Header.Set("Origin", origin)
	uploadReq.Header.Set("X-CSRF-Token", csrf)
	uploadReq.Header.Set("Content-Type", form.FormDataContentType())
	uploaded, err := client.Do(uploadReq)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		LatestVersion domain.DocumentVersion `json:"latest_version"`
	}
	err = json.NewDecoder(uploaded.Body).Decode(&details)
	uploaded.Body.Close()
	if err != nil || uploaded.StatusCode != 201 || !details.LatestVersion.IngestionJobID.Valid {
		t.Fatal("upload without AI failed", err)
	}
	jobResponse, err := client.Get(origin + "/api/v1/ingestion-jobs/" + details.LatestVersion.IngestionJobID.UUID.String())
	if err != nil {
		t.Fatal(err)
	}
	var job domain.IngestionJob
	err = json.NewDecoder(jobResponse.Body).Decode(&job)
	jobResponse.Body.Close()
	if err != nil || jobResponse.StatusCode != 200 || job.Status != domain.JobPending || job.Attempts != 0 {
		t.Fatal("worker ran without AI configuration", err)
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
	// Restart with configured but failing Python: API still starts and recovers
	// the previously queued upload through the real main worker wiring.
	python := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer python.Close()
	t.Setenv("AI_SERVICE_URL", python.URL)
	t.Setenv("AI_SERVICE_TOKEN", "entrypoint-test-service-token")
	t.Setenv("INGESTION_POLL_INTERVAL", "20ms")
	t.Setenv("INGESTION_REQUEST_TIMEOUT", "2s")
	restartedCtx, restartedCancel := context.WithCancel(context.Background())
	restartedResult := make(chan error, 1)
	go func() { restartedResult <- run(restartedCtx, nil) }()
	restartStopped := false
	defer func() {
		restartedCancel()
		if !restartStopped {
			select {
			case <-restartedResult:
			case <-time.After(12 * time.Second):
				t.Error("restarted entrypoint did not stop")
			}
		}
	}()
	restartDeadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-restartedResult:
			restartStopped = true
			t.Fatal("entrypoint failed with unavailable Python", err)
		default:
		}
		response, err := client.Get(origin + "/api/v1/auth/me")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == 401 {
				break
			}
		}
		if time.Now().After(restartDeadline) {
			t.Fatal("API did not restart with unavailable Python")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if response := request("POST", "/api/v1/auth/login", string(payload), ""); response.StatusCode != 200 {
		t.Fatal("restarted authentication failed")
	}
	for {
		response, err := client.Get(origin + "/api/v1/ingestion-jobs/" + details.LatestVersion.IngestionJobID.UUID.String())
		if err != nil {
			t.Fatal(err)
		}
		var retried domain.IngestionJob
		err = json.NewDecoder(response.Body).Decode(&retried)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if retried.Status == domain.JobPending && retried.Attempts > 0 {
			break
		}
		if time.Now().After(restartDeadline) {
			t.Fatal("main worker did not retry unavailable Python")
		}
		time.Sleep(20 * time.Millisecond)
	}
	restartedCancel()
	select {
	case err := <-restartedResult:
		restartStopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("restarted graceful shutdown timed out")
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
