package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

type fakeAuthenticator struct {
	identity service.Identity
	err      error
	calls    int
}

func (f *fakeAuthenticator) Authenticate(context.Context, string) (service.Identity, error) {
	f.calls++
	return f.identity, f.err
}

func TestRequireAuth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cookies int
		err     error
		status  int
	}{{"absent", 0, nil, 401}, {"valid", 1, nil, 200}, {"invalid", 1, service.ErrUnauthorized, 401}, {"database", 1, errors.New("DB failed"), 500}, {"duplicate", 2, nil, 401}} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuthenticator{identity: service.Identity{User: domain.User{Username: "member"}}, err: tc.err}
			h := RequireAuth(auth, zap.NewNop(), "ai_session")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := Current(r.Context()); !ok {
					t.Error("identity missing from context")
				}
				w.WriteHeader(200)
			}))
			req := httptest.NewRequest("GET", "/", nil)
			for range tc.cookies {
				req.AddCookie(&http.Cookie{Name: "ai_session", Value: "token"})
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("got %d", response.Code)
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	for _, tc := range []struct {
		role     domain.Role
		identity bool
		status   int
	}{{domain.RoleAdmin, true, 200}, {domain.RoleMember, true, 403}, {"", false, 401}} {
		h := RequireRole(zap.NewNop(), domain.RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
		req := httptest.NewRequest("GET", "/", nil)
		if tc.identity {
			req = req.WithContext(context.WithValue(req.Context(), identityKey{}, service.Identity{User: domain.User{Role: tc.role}}))
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("role %s: status=%d", tc.role, response.Code)
		}
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	origins, err := NewOriginPolicy([]string{"https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hash, _ := service.HashToken(token)
	for _, tc := range []struct {
		name, method, origin, token string
		status                      int
	}{
		{"GET skips CSRF", "GET", "", "", 204},
		{"valid", "POST", "https://app.example", token, 204},
		{"missing", "POST", "https://app.example", "", 403},
		{"invalid", "POST", "https://app.example", "wrong", 403},
		{"foreign origin", "POST", "https://evil.example", token, 403},
		{"missing origin", "POST", "", token, 403},
		{"null origin", "POST", "null", token, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.token != "" {
				req.Header.Set("X-CSRF-Token", tc.token)
			}
			req = req.WithContext(context.WithValue(req.Context(), identityKey{}, service.Identity{Session: domain.Session{CSRFTokenHash: hash}}))
			h := origins.CSRF(zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	for _, origin := range []string{"*", "https://*.example", "https://app.example/path", "null"} {
		if _, err := NewOriginPolicy([]string{origin}); err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
}

func TestCORSPreflight(t *testing.T) {
	policy, _ := NewOriginPolicy([]string{"http://localhost:5173"})
	h := policy.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("preflight reached handler") }))
	req := httptest.NewRequest("OPTIONS", "/api/v1/auth/logout", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-CSRF-Token")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 204 || response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("trusted credentialed preflight failed")
	}
	req.Header.Set("Origin", "http://evil.example")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 403 || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted CORS origin accepted")
	}
}

func TestLoginRateLimitIgnoresForwardedIP(t *testing.T) {
	h := LoginRateLimit(zap.NewNop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	for i := range 11 {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if i < 10 && response.Code != 401 {
			t.Fatal("early rate limit")
		}
		if i == 10 && (response.Code != 429 || response.Header().Get("Retry-After") == "") {
			t.Fatal("missing rate limit")
		}
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "192.0.2.2:1234"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatal("second peer shared the first peer's limit")
	}
}
