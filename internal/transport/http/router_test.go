package httptransport

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type routerAuth struct {
	result  service.LoginResult
	revoked bool
}

func (f *routerAuth) Login(context.Context, string, string) (service.LoginResult, error) {
	f.revoked = false
	return f.result, nil
}
func (f *routerAuth) Logout(context.Context, uuid.UUID) error { f.revoked = true; return nil }
func (f *routerAuth) Authenticate(ctx context.Context, token string) (service.Identity, error) {
	if token != f.result.SessionToken || f.revoked {
		return service.Identity{}, service.ErrUnauthorized
	}
	return service.Identity{User: f.result.User, Session: f.result.Session}, nil
}

func routerFixture(t *testing.T) (http.Handler, *routerAuth) {
	t.Helper()
	sessionToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	csrfBytes := make([]byte, 32)
	csrfBytes[0] = 1
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfBytes)
	csrfHash, _ := service.HashToken(csrfToken)
	auth := &routerAuth{result: service.LoginResult{User: domain.User{ID: uuid.New(), Username: "admin", Role: domain.RoleAdmin}, Session: domain.Session{ID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour), CSRFTokenHash: csrfHash}, SessionToken: sessionToken, CSRFToken: csrfToken}}
	h, err := NewRouter(auth, zap.NewNop(), RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	return h, auth
}

func TestRouterLoginMeLogout(t *testing.T) {
	h, auth := routerFixture(t)
	request := func(method, path, body, csrf string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", "http://localhost:8080")
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/api/v1/auth/me", "", "", nil); w.Code != 401 {
		t.Fatal("anonymous me accepted")
	}
	login := request("POST", "/api/v1/auth/login", `{"username":"admin","password":"test-password"}`, "", nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookies := login.Result().Cookies()
	if w := request("GET", "/api/v1/auth/me", "", "", cookies); w.Code != 200 || strings.Contains(w.Body.String(), "token") {
		t.Fatal("me failed or exposed a token")
	}
	if w := request("GET", "/api/v1/admin/ping", "", "", cookies); w.Code != 200 {
		t.Fatal("admin role denied")
	}
	auth.result.User.Role = domain.RoleMember
	if w := request("GET", "/api/v1/admin/ping", "", "", cookies); w.Code != 403 {
		t.Fatal("member passed admin role")
	}
	for _, csrf := range []string{"", "wrong"} {
		if w := request("POST", "/api/v1/auth/logout", "", csrf, cookies); w.Code != 403 || auth.revoked {
			t.Fatal("invalid CSRF permitted logout")
		}
	}
	logout := request("POST", "/api/v1/auth/logout", "", auth.result.CSRFToken, cookies)
	if logout.Code != 204 || !auth.revoked {
		t.Fatal("logout did not revoke session")
	}
	for _, cookie := range logout.Result().Cookies() {
		if cookie.MaxAge != -1 || cookie.Path != "/" {
			t.Fatal("logout cookie not deleted")
		}
	}
	if w := request("GET", "/api/v1/auth/me", "", "", cookies); w.Code != 401 {
		t.Fatal("revoked cookie still authenticates")
	}
	if w := request("POST", "/api/v1/auth/logout", "", "", cookies); w.Code != 204 {
		t.Fatal("repeated logout failed")
	}
	if w := request("POST", "/api/v1/auth/logout", "", "", nil); w.Code != 204 {
		t.Fatal("anonymous repeated logout failed")
	}
}

func TestProductionRequiresRealTLS(t *testing.T) {
	_, auth := routerFixture(t)
	h, err := NewRouter(auth, zap.NewNop(), RouterConfig{Production: true, CookieSecure: true, AllowedOrigins: []string{"https://app.example"}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "http://app.example/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("forwarded header bypassed HTTPS requirement")
	}
	req = httptest.NewRequest("POST", "https://app.example/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if !cookie.Secure || !strings.HasPrefix(cookie.Name, "__Host-") || cookie.Domain != "" {
			t.Fatal("unsafe production cookie")
		}
	}
}
