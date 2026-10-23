package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type moduleUsers struct {
	creates, lists int
	member         domain.User
}

func (f *moduleUsers) Create(_ context.Context, u domain.User) (domain.User, error) {
	f.creates++
	u.ID = uuid.New()
	return u, nil
}
func (f *moduleUsers) List(context.Context, int, int) ([]domain.User, int64, error) {
	f.lists++
	return []domain.User{f.member}, 1, nil
}
func (f *moduleUsers) GetByID(context.Context, uuid.UUID) (domain.User, error) { return f.member, nil }

type moduleDocuments struct {
	service.Documents
	creates int
	doc     domain.Document
	version domain.DocumentVersion
}

func (f *moduleDocuments) CreateDocumentWithVersion(_ context.Context, d domain.Document, v domain.DocumentVersion) (domain.Document, domain.DocumentVersion, error) {
	f.creates++
	d.ID = uuid.New()
	v.ID = uuid.New()
	v.DocumentID = d.ID
	v.VersionNumber = 1
	d.ActiveVersionID = uuid.NullUUID{UUID: v.ID, Valid: true}
	f.doc = d
	f.version = v
	return d, v, nil
}
func (f *moduleDocuments) GetByID(context.Context, uuid.UUID) (domain.Document, error) {
	return f.doc, nil
}
func (f *moduleDocuments) GetLatestVersion(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return f.version, nil
}
func (f *moduleDocuments) GetVersionByID(context.Context, uuid.UUID) (domain.DocumentVersion, error) {
	return f.version, nil
}

type moduleACL struct{ service.DocumentAccessStore }

func (f *moduleACL) HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }

func TestModuleRoutesAuthorizationAndValidation(t *testing.T) {
	_, auth := routerFixture(t)
	auth.result.User.IsActive = true
	users := &moduleUsers{member: domain.User{ID: uuid.New(), Username: "employee", Role: domain.RoleMember, IsActive: true, PasswordHash: "private-hash"}}
	docs := &moduleDocuments{}
	log := zap.NewNop()
	access := service.NewDocumentAccessService(&moduleACL{}, docs, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	h, err := NewRouter(auth, log, RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}}, Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, contentType, csrf string, cookie bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("Content-Type", contentType)
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: "ai_session", Value: auth.result.SessionToken})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	id := uuid.NewString()
	routes := []struct {
		method, path string
		admin        bool
	}{
		{"POST", "/api/v1/users", true}, {"GET", "/api/v1/users", true}, {"POST", "/api/v1/documents", true},
		{"GET", "/api/v1/documents", false}, {"GET", "/api/v1/documents/" + id, false}, {"GET", "/api/v1/documents/" + id + "/download", false},
		{"GET", "/api/v1/documents/" + id + "/access", true}, {"POST", "/api/v1/documents/" + id + "/access", true}, {"DELETE", "/api/v1/documents/" + id + "/access/" + users.member.ID.String(), true},
		{"POST", "/api/v1/documents/" + id + "/versions", true},
		{"GET", "/api/v1/documents/" + id + "/versions", false},
		{"GET", "/api/v1/documents/" + id + "/versions/" + uuid.NewString(), false},
		{"GET", "/api/v1/documents/" + id + "/versions/" + uuid.NewString() + "/download", false},
	}
	for _, route := range routes {
		if w := request(route.method, route.path, "{}", "application/json", "", false); w.Code != 401 {
			t.Fatalf("anonymous %s %s: %d", route.method, route.path, w.Code)
		}
		if route.admin {
			auth.result.User.Role = domain.RoleMember
			if w := request(route.method, route.path, "{}", "application/json", auth.result.CSRFToken, true); w.Code != 403 {
				t.Fatalf("member %s %s: %d", route.method, route.path, w.Code)
			}
			auth.result.User.Role = domain.RoleAdmin
		}
		if route.method != "GET" {
			for _, bad := range []string{"", "bad"} {
				if w := request(route.method, route.path, "{}", "application/json", bad, true); w.Code != 403 {
					t.Fatalf("CSRF bypass %s: %d", route.path, w.Code)
				}
			}
		}
	}
	if users.creates != 0 || docs.creates != 0 {
		t.Fatal("denied request reached service write")
	}
	for _, payload := range []string{`{"username":"member","password":"strong-password-123","role":"admin"}`, `{"username":"ab","password":"strong-password-123"}`, `{} {}`, `null`} {
		if w := request("POST", "/api/v1/users", payload, "application/json", auth.result.CSRFToken, true); w.Code != 400 {
			t.Fatalf("invalid users request: %d", w.Code)
		}
	}
	w := request("POST", "/api/v1/users", `{"username":"member","password":"strong-password-123"}`, "application/json", auth.result.CSRFToken, true)
	if w.Code != 201 || strings.Contains(w.Body.String(), "password") || !strings.Contains(w.Body.String(), `"role":"member"`) {
		t.Fatalf("create response: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/users", "", "", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-hash") || strings.Contains(w.Body.String(), "password") {
		t.Fatal("list leaked hash")
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("title", "Policy")
	part, _ := mw.CreateFormFile("file", `..\policy.txt`)
	part.Write([]byte("original text"))
	mw.Close()
	w = request("POST", "/api/v1/documents", b.String(), mw.FormDataContentType(), auth.result.CSRFToken, true)
	if w.Code != 201 || strings.Contains(w.Body.String(), "storage_key") {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var details service.DocumentDetails
	if err := json.Unmarshal(w.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/v1/documents/"+details.ID.String()+"/download", "", "", "", true)
	if w.Code != 200 || w.Body.String() != "original text" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	auth.result.User.Role = domain.RoleMember
	for _, suffix := range []string{"", "/download"} {
		w = request("GET", "/api/v1/documents/"+details.ID.String()+suffix, "", "", "", true)
		if w.Code != 404 {
			t.Fatal("direct member request bypassed ACL")
		}
	}
	auth.result.User.Role = domain.RoleAdmin
	w = request("POST", "/api/v1/documents", "bad", "multipart/form-data", auth.result.CSRFToken, true)
	if w.Code != 400 {
		t.Fatalf("invalid multipart: %d", w.Code)
	}
	for _, size := range []int{1025, (1 << 20) + 2048} {
		b.Reset()
		mw = multipart.NewWriter(&b)
		if err := mw.WriteField("title", "Too large"); err != nil {
			t.Fatal(err)
		}
		part, err = mw.CreateFormFile("file", "large.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		w = request("POST", "/api/v1/documents", b.String(), mw.FormDataContentType(), auth.result.CSRFToken, true)
		if w.Code != 413 {
			t.Fatalf("file/request size %d: got %d", size, w.Code)
		}
	}
}
