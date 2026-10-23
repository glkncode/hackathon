package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"go.uber.org/zap"
)

func TestIntegrationIngestionHTTPWorkerRecovery(t *testing.T) {
	db, ctx := testDatabase(t)
	log := zap.NewNop()
	users := postgres.NewUserRepository(db)
	if err := service.BootstrapAdmin(ctx, users, log, service.BootstrapConfig{Enabled: true, Username: "admin", Password: "integration-password-123"}); err != nil {
		t.Fatal(err)
	}
	auth, err := service.NewAuthService(users, postgres.NewSessionRepository(db), log, service.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	storage, err := service.NewLocalDocumentStorage(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	repo := postgres.NewDocumentRepository(db)
	jobs := postgres.NewIngestionRepository(db)
	chunks := postgres.NewChunkRepository(db)
	access := service.NewDocumentAccessService(postgres.NewDocumentAccessRepository(db), repo, users, log)
	documents := service.NewDocumentService(repo, access, storage, log)
	ingestion, err := service.NewIngestionService(jobs, 3)
	if err != nil {
		t.Fatal(err)
	}
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: documents, Access: access, Ingestion: ingestion, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(router)
	defer api.Close()
	jar, _ := cookiejar.New(nil)
	admin := &documentClient{t: t, ctx: ctx, base: api.URL, client: &http.Client{Jar: jar}}
	admin.login("admin")
	var details service.DocumentDetails
	if err := json.Unmarshal(admin.upload("rules.txt", []byte("Internal rules"), 201), &details); err != nil {
		t.Fatal(err)
	}
	first := details.LatestVersion
	if !first.IngestionJobID.Valid {
		t.Fatal("upload did not expose job ID")
	}
	path := func(v domain.DocumentVersion) string {
		return "/api/v1/ingestion-jobs/" + v.IngestionJobID.UUID.String()
	}
	loadJob := func(v domain.DocumentVersion) domain.IngestionJob {
		t.Helper()
		var job domain.IngestionJob
		if err := json.Unmarshal(admin.json("GET", path(v), "", 200), &job); err != nil {
			t.Fatal(err)
		}
		return job
	}
	if j := loadJob(first); j.Status != domain.JobPending || j.Attempts != 0 {
		t.Fatal("no-AI upload did not remain queued")
	}
	admin.json("GET", "/api/v1/auth/me", "", 200)
	var status atomic.Int32
	status.Store(200)
	started := make(chan struct{}, 1)
	releasePython := make(chan struct{})
	python := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Token") != "private-test-token" {
			t.Error("wrong service authentication")
			w.WriteHeader(401)
			return
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		io.Copy(io.Discard, r.Body)
		versionID := r.FormValue("document_version_id")
		if status.Load() == -1 {
			started <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-releasePython:
			}
			return
		}
		if code := int(status.Load()); code != 200 {
			w.WriteHeader(code)
			io.WriteString(w, "private raw error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"document_version_id": versionID, "chunks": []map[string]any{{"chunk_index": 0, "content": "Порядок согласования", "page_number": nil, "section_number": nil, "section_title": nil}}, "warnings": []string{}})
	}))
	defer python.Close()
	defer close(releasePython)
	client, err := aiclient.New(aiclient.Config{URL: python.URL, Token: "private-test-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	worker, err := service.NewIngestionWorker(jobs, repo, storage, client, log, service.WorkerConfig{PollInterval: time.Millisecond, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := loadJob(first); j.Status != domain.JobCompleted || j.Attempts != 1 || j.CompletedAt == nil {
		t.Fatal("completion not visible")
	}
	if c, err := chunks.GetChunksByVersion(ctx, first.ID); err != nil || len(c) != 1 || c[0].DocumentVersionID != first.ID {
		t.Fatal("chunks not version-bound")
	}
	uploadVersion := func(text string) domain.DocumentVersion {
		t.Helper()
		body, media, err := versionMultipart("next.txt", []byte(text), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := admin.request("POST", "/api/v1/documents/"+details.ID.String()+"/versions", body, media, admin.csrf, 201)
		var v domain.DocumentVersion
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	second := uploadVersion("Version two")
	status.Store(422)
	if err := worker.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := loadJob(second); j.Status != domain.JobFailed || j.LastError != "ai_rejected" {
		t.Fatal("permanent rejection not failed")
	}
	active, err := repo.GetActiveVersion(ctx, details.ID)
	if err != nil || active.ID != first.ID {
		t.Fatal("failure changed active original")
	}
	admin.request("POST", path(second)+"/retry", nil, "", "", 403)
	admin.json("POST", path(second)+"/retry", "", 200)
	admin.json("POST", path(second)+"/retry", "", 409)
	status.Store(200)
	if err := worker.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	active, err = repo.GetActiveVersion(ctx, details.ID)
	if err != nil || active.ID != second.ID || active.Status != domain.VersionReady {
		t.Fatal("retried version not published")
	}
	third := uploadVersion("Version three")
	status.Store(503)
	if err := worker.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := loadJob(third); j.Status != domain.JobPending || j.Attempts != 1 || j.LastError != "ai_rejected" {
		t.Fatal("unavailable Python not retried")
	}
	admin.json("GET", "/api/v1/auth/me", "", 200)
	if _, err := db.ExecContext(ctx, `UPDATE ingestion_jobs SET next_attempt_at=clock_timestamp() WHERE id=$1`, third.IngestionJobID.UUID); err != nil {
		t.Fatal(err)
	}
	// Stop during a real request; a replacement process recovers the expired lease.
	status.Store(-1)
	workerCtx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() { defer close(finished); worker.Run(workerCtx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker shutdown blocked")
	}
	if j := loadJob(third); j.Status != domain.JobProcessing {
		t.Fatal("shutdown falsely completed job")
	}
	if _, err := db.ExecContext(ctx, `UPDATE ingestion_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, third.IngestionJobID.UUID); err != nil {
		t.Fatal(err)
	}
	status.Store(200)
	if err := worker.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := loadJob(third); j.Status != domain.JobCompleted || j.Attempts != 3 {
		t.Fatal("crash recovery lost job")
	}
	if _, err := jobs.ClaimNextJob(ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("completed job still available")
	}
	admin.json("POST", "/api/v1/users", `{"username":"member","password":"integration-password-123"}`, 201)
	memberJar, _ := cookiejar.New(nil)
	member := &documentClient{t: t, ctx: ctx, base: api.URL, client: &http.Client{Jar: memberJar}}
	member.login("member")
	member.json("GET", path(first), "", 403)
	member.json("POST", path(first)+"/retry", "", 403)
	// A failed job insert must roll back metadata AND clean up the new disk file.
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_enqueue() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER reject_enqueue BEFORE INSERT ON ingestion_jobs FOR EACH ROW EXECUTE FUNCTION reject_enqueue()`); err != nil {
		t.Fatal(err)
	}
	data := admin.upload("rollback.txt", []byte("New private document"), 500)
	if strings.Contains(string(data), "test failure") {
		t.Fatal("database error leaked")
	}
	after, err := os.ReadDir(dir)
	if err != nil || len(after) != len(before) {
		t.Fatal("failed transaction leaked file")
	}
}
