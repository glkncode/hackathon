package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"ai-assistant/internal/transport/http/middleware"
	"github.com/google/uuid"
)

type Auth interface {
	Login(context.Context, string, string) (service.LoginResult, error)
	Logout(context.Context, uuid.UUID) error
}

type CookieConfig struct{ Secure bool }

func (c CookieConfig) SessionName() string {
	if c.Secure {
		return "__Host-ai_session"
	}
	return "ai_session"
}
func (c CookieConfig) CSRFName() string {
	if c.Secure {
		return "__Host-ai_csrf"
	}
	return "ai_csrf"
}

type AuthHandler struct {
	auth    Auth
	cookies CookieConfig
}

func NewAuth(auth Auth, cookies CookieConfig) *AuthHandler {
	return &AuthHandler{auth: auth, cookies: cookies}
}

type publicUser struct {
	ID       uuid.UUID   `json:"id"`
	Username string      `json:"username"`
	Role     domain.Role `json:"role"`
}

func public(user domain.User) publicUser {
	return publicUser{ID: user.ID, Username: user.Username, Role: user.Role}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		middleware.JSONError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		middleware.JSONError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		middleware.JSONError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if strings.TrimSpace(input.Username) == "" || utf8.RuneCountInString(input.Username) > 64 || input.Password == "" {
		middleware.JSONError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	result, err := h.auth.Login(r.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			middleware.JSONError(w, http.StatusUnauthorized, "invalid credentials")
		} else {
			middleware.JSONError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	maxAge := int(math.Ceil(time.Until(result.Session.ExpiresAt).Seconds()))
	if maxAge < 1 {
		middleware.JSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.setCookie(w, h.cookies.SessionName(), result.SessionToken, true, result.Session.ExpiresAt, maxAge)
	h.setCookie(w, h.cookies.CSRFName(), result.CSRFToken, false, result.Session.ExpiresAt, maxAge)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		User publicUser `json:"user"`
	}{User: public(result.User)})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.Current(r.Context())
	if !ok {
		middleware.JSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(public(identity.User))
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if identity, ok := middleware.Current(r.Context()); ok {
		if err := h.auth.Logout(r.Context(), identity.Session.ID); err != nil {
			middleware.JSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	h.setCookie(w, h.cookies.SessionName(), "", true, time.Unix(1, 0), -1)
	h.setCookie(w, h.cookies.CSRFName(), "", false, time.Unix(1, 0), -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: h.cookies.Secure,
		SameSite: http.SameSiteLaxMode, Expires: expires.UTC(), MaxAge: maxAge})
}
