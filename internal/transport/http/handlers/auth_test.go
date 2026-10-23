package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
)

type fakeAuth struct {
	result service.LoginResult
	err    error
	calls  int
}

func (f *fakeAuth) Login(context.Context, string, string) (service.LoginResult, error) {
	f.calls++
	return f.result, f.err
}
func (f *fakeAuth) Logout(context.Context, uuid.UUID) error { return f.err }

func TestLoginCookieAttributesAndPublicResponse(t *testing.T) {
	for _, secure := range []bool{false, true} {
		auth := &fakeAuth{result: service.LoginResult{User: domain.User{ID: uuid.New(), Username: "admin", Role: domain.RoleAdmin, PasswordHash: "private-password-hash"}, Session: domain.Session{ExpiresAt: time.Now().Add(12 * time.Hour)}, SessionToken: "private-session-token", CSRFToken: "private-csrf-token"}}
		h := NewAuth(auth, CookieConfig{Secure: secure})
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct-password"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.Login(response, req)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		cookies := response.Result().Cookies()
		if len(cookies) != 2 {
			t.Fatal("expected session and CSRF cookies")
		}
		for _, cookie := range cookies {
			if cookie.Secure != secure || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge < 43198 || cookie.Expires.Before(time.Now()) {
				t.Fatal("unsafe cookie attributes")
			}
			if cookie.Name == h.cookies.SessionName() && !cookie.HttpOnly {
				t.Fatal("session cookie is not HttpOnly")
			}
			if cookie.Name == h.cookies.CSRFName() && cookie.HttpOnly {
				t.Fatal("CSRF cookie must be readable by client JavaScript")
			}
			if strings.HasPrefix(cookie.Name, "__Host-") != secure {
				t.Fatal("invalid host cookie prefix")
			}
		}
		for _, secret := range []string{"private-session-token", "private-csrf-token", "private-password-hash", "password_hash"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("secret in login JSON")
			}
		}
	}
}

func TestLoginRejectsMalformedJSON(t *testing.T) {
	for _, body := range []string{"", `{`, `null`, `{"username":"a"}`, `{"username":"a","password":"b","extra":true}`, `{"username":"a","password":"b"} {}`, strings.Repeat("x", 5000)} {
		auth := &fakeAuth{}
		h := NewAuth(auth, CookieConfig{})
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.Login(response, req)
		if response.Code != 400 || auth.calls != 0 {
			t.Fatalf("invalid JSON was accepted: status=%d", response.Code)
		}
	}
}

func TestLoginErrorsDoNotLeakDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{service.ErrInvalidCredentials, 401}, {errors.New("database detail"), 500}} {
		h := NewAuth(&fakeAuth{err: tc.err}, CookieConfig{})
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.Login(response, req)
		if response.Code != tc.status || strings.Contains(response.Body.String(), "database detail") {
			t.Fatal("incorrect HTTP error mapping")
		}
	}
}
