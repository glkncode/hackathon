package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type httpSearchStore struct {
	actorID uuid.UUID
	role    domain.Role
	calls   int
	results []domain.SearchResult
	err     error
}

func (s *httpSearchStore) Search(_ context.Context, id uuid.UUID, role domain.Role, _ domain.SearchQuery) ([]domain.SearchResult, error) {
	s.actorID, s.role = id, role
	s.calls++
	return s.results, s.err
}

func TestSearchRouteAuthenticationValidationAndSources(t *testing.T) {
	_, auth := routerFixture(t)
	auth.result.User.IsActive = true
	auth.result.User.Role = domain.RoleMember
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)
	documents, users := &moduleDocuments{}, &moduleUsers{}
	access := service.NewDocumentAccessService(&moduleACL{}, documents, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	store := &httpSearchStore{}
	handler, err := NewRouter(auth, log, RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}}, Modules{
		Users: service.NewUserService(users), Documents: service.NewDocumentService(documents, access, storage, log),
		Access: access, MaxUploadBytes: 1024, Search: service.NewSearchService(store),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(body, contentType, origin, csrf string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/search", strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "ai_session", Value: auth.result.SessionToken})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const body = `{"query":"секретный поисковый запрос","limit":10}`
	const origin = "http://localhost:8080"
	for _, tc := range []struct {
		name, origin, csrf string
		cookie             bool
		status             int
	}{
		{"anonymous", origin, "", false, 401},
		{"missing CSRF", origin, "", true, 403},
		{"invalid CSRF", origin, "wrong", true, 403},
		{"invalid origin", "https://evil.example", auth.result.CSRFToken, true, 403},
		{"missing origin", "", auth.result.CSRFToken, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := request(body, "application/json", tc.origin, tc.csrf, tc.cookie); w.Code != tc.status {
				t.Fatalf("status=%d, want=%d", w.Code, tc.status)
			}
		})
	}
	if store.calls != 0 {
		t.Fatal("unauthorized request reached search")
	}
	for _, bad := range []string{
		``, `{`, `[]`, `null`, `{}`, `{"query":" "}`, `{"query":5}`, `{"query":"x","limit":-1}`,
		`{"query":"x","limit":31}`, `{"query":"x","limit":1.5}`, `{"query":"x"} {}`,
		`{"query":"x","role":"admin"}`, `{"query":"x","user_id":"` + uuid.NewString() + `"}`,
		`{"query":"` + strings.Repeat("я", 501) + `"}`, `{"query":"\u0000"}`, strings.Repeat(" ", 4097),
	} {
		if w := request(bad, "application/json", origin, auth.result.CSRFToken, true); w.Code != 400 {
			t.Fatalf("invalid JSON/parameters accepted: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if w := request(body, "text/plain", origin, auth.result.CSRFToken, true); w.Code != 400 {
		t.Fatal("invalid content type accepted")
	}
	if store.calls != 0 {
		t.Fatal("invalid input reached search")
	}
	w := request(body, "application/json", origin, auth.result.CSRFToken, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"results":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal("empty search response", w.Code, w.Body.String())
	}
	store.results = []domain.SearchResult{{ChunkID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(), DocumentTitle: "Инструкция", VersionNumber: 2, ChunkIndex: 3, Content: `<script>alert("source")</script>`, Score: 0.123456789}}
	w = request(body, "application/json", origin, auth.result.CSRFToken, true)
	var response struct {
		Query, Mode string
		Results     []domain.SearchResult
		Total       int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Mode != "search" || response.Total != 1 || len(response.Results) != 1 {
		t.Fatal("search format", w.Body.String(), err)
	}
	got := response.Results[0]
	if got.ChunkID != store.results[0].ChunkID || got.DocumentVersionID != store.results[0].DocumentVersionID || got.Content != store.results[0].Content || got.Score != store.results[0].Score || got.PageNumber != nil || got.SectionNumber != nil || got.SectionTitle != nil {
		t.Fatal("citation metadata or relevance changed")
	}
	if store.actorID != auth.result.User.ID || store.role != domain.RoleMember || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "storage_key") {
		t.Fatal("identity or response safety failed")
	}
	store.err = errors.New("database unavailable")
	w = request(body, "application/json", origin, auth.result.CSRFToken, true)
	if w.Code != 500 || strings.Contains(w.Body.String(), "database") {
		t.Fatal("repository failure not hidden")
	}
	for _, entry := range logs.All() {
		encoded, _ := json.Marshal(entry.ContextMap())
		if strings.Contains(entry.Message+string(encoded), "секретный поисковый запрос") || strings.Contains(entry.Message+string(encoded), "<script>") {
			t.Fatal("query or chunk text logged")
		}
	}
}
