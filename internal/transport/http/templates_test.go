package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestTemplateRoutesRequireAuthenticationAndCSRF(t *testing.T) {
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
	h, err := NewRouter(auth, log, RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}}, Modules{Templates: service.NewTemplateService(nil, docs, access, storage, nil, log), Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	for _, route := range []struct {
		method, path string
		admin        bool
	}{
		{"GET", "/api/v1/templates", false}, {"POST", "/api/v1/templates", true}, {"GET", "/api/v1/templates/" + id, false}, {"PATCH", "/api/v1/templates/" + id, true},
		{"POST", "/api/v1/templates/" + id + "/drafts", false}, {"GET", "/api/v1/template-drafts", false}, {"GET", "/api/v1/template-drafts/" + id, false},
		{"PATCH", "/api/v1/template-drafts/" + id, false}, {"GET", "/api/v1/template-drafts/" + id + "/next-question", false}, {"POST", "/api/v1/template-drafts/" + id + "/answers", false},
		{"POST", "/api/v1/template-drafts/" + id + "/validate", false}, {"POST", "/api/v1/template-drafts/" + id + "/generate", false}, {"GET", "/api/v1/generated-documents/" + id, false}, {"GET", "/api/v1/generated-documents/" + id + "/download", false},
	} {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("anonymous %s %s: %d", route.method, route.path, w.Code)
		}
		if route.method != "GET" {
			r.AddCookie(&http.Cookie{Name: "ai_session", Value: auth.result.SessionToken})
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("CSRF %s: %d", route.path, w.Code)
			}
		}
		if route.admin {
			auth.result.User.Role = domain.RoleMember
			r.Header.Set("X-CSRF-Token", auth.result.CSRFToken)
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("member reached admin operation")
			}
			auth.result.User.Role = domain.RoleAdmin
		}
	}
	preflight := httptest.NewRequest("OPTIONS", "/api/v1/template-drafts/"+id, nil)
	preflight.Header.Set("Origin", "http://localhost:8080")
	preflight.Header.Set("Access-Control-Request-Method", "PATCH")
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type, X-CSRF-Token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, preflight)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Fatal("frontend PATCH preflight blocked")
	}
}
