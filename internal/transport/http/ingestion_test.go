package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ingestionJobsStub struct {
	job   domain.IngestionJob
	calls int
}

func (s *ingestionJobsStub) GetJob(_ context.Context, id uuid.UUID) (domain.IngestionJob, error) {
	s.calls++
	if id != s.job.ID {
		return domain.IngestionJob{}, domain.ErrNotFound
	}
	return s.job, nil
}
func (s *ingestionJobsStub) RetryFailedJob(_ context.Context, id uuid.UUID, _ int) (domain.IngestionJob, error) {
	s.calls++
	if id != s.job.ID {
		return domain.IngestionJob{}, domain.ErrNotFound
	}
	if s.job.Status != domain.JobFailed {
		return domain.IngestionJob{}, domain.ErrInvalidState
	}
	s.job.Status = domain.JobPending
	return s.job, nil
}
func TestIngestionRoutesRBACAndCSRF(t *testing.T) {
	_, auth := routerFixture(t)
	auth.result.User.IsActive = true
	log := zap.NewNop()
	docs := &moduleDocuments{}
	users := &moduleUsers{}
	access := service.NewDocumentAccessService(&moduleACL{}, docs, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	jobs := &ingestionJobsStub{job: domain.IngestionJob{ID: uuid.New(), DocumentVersionID: uuid.New(), Status: domain.JobFailed, LastError: "ai_unavailable", LeaseToken: uuid.NullUUID{UUID: uuid.New(), Valid: true}}}
	ingestion, err := service.NewIngestionService(jobs, 5)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewRouter(auth, log, RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}}, Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, Ingestion: ingestion, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, origin, csrf string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "ai_session", Value: auth.result.SessionToken})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/ingestion-jobs/" + jobs.job.ID.String()
	for _, method := range []string{"GET", "POST"} {
		route := path
		if method == "POST" {
			route += "/retry"
		}
		if w := request(method, route, "http://localhost:8080", auth.result.CSRFToken, false); w.Code != 401 {
			t.Fatal("anonymous accepted", w.Code)
		}
		auth.result.User.Role = domain.RoleMember
		if w := request(method, route, "http://localhost:8080", auth.result.CSRFToken, true); w.Code != 403 {
			t.Fatal("member accepted", w.Code)
		}
		auth.result.User.Role = domain.RoleAdmin
	}
	if jobs.calls != 0 {
		t.Fatal("unauthorized repository call")
	}
	for _, tc := range []struct{ origin, csrf string }{{"http://localhost:8080", ""}, {"https://evil.example", auth.result.CSRFToken}, {"", auth.result.CSRFToken}} {
		if w := request("POST", path+"/retry", tc.origin, tc.csrf, true); w.Code != 403 {
			t.Fatal("CSRF accepted", w.Code)
		}
	}
	if w := request("GET", path, "http://localhost:8080", "", true); w.Code != 200 || strings.Contains(w.Body.String(), "lease_token") || strings.Contains(w.Body.String(), jobs.job.LeaseToken.UUID.String()) {
		t.Fatal("job GET failed or leaked lease", w.Body.String())
	}
	if w := request("POST", path+"/retry", "http://localhost:8080", auth.result.CSRFToken, true); w.Code != 200 {
		t.Fatal("retry failed", w.Body.String())
	}
	if w := request("POST", path+"/retry", "http://localhost:8080", auth.result.CSRFToken, true); w.Code != 409 {
		t.Fatal("pending retry accepted", w.Code)
	}
	if w := request("GET", "/api/v1/ingestion-jobs/"+uuid.NewString(), "http://localhost:8080", "", true); w.Code != 404 {
		t.Fatal("missing job status", w.Code)
	}
	// The service also enforces admin when invoked outside HTTP middleware.
	if _, err := ingestion.Get(context.Background(), domain.User{Role: domain.RoleMember, IsActive: true}, jobs.job.ID); err != domain.ErrAccessDenied {
		t.Fatal("service RBAC missing")
	}
}
