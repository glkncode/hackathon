package httptransport

import (
	"errors"
	"net/http"
	"strings"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/transport/http/handlers"
	"ai-assistant/internal/transport/http/middleware"
	"go.uber.org/zap"
)

type Auth interface {
	handlers.Auth
	middleware.Authenticator
}

type RouterConfig struct {
	Production     bool
	CookieSecure   bool
	AllowedOrigins []string
}

func NewRouter(auth Auth, log *zap.Logger, cfg RouterConfig) (http.Handler, error) {
	if auth == nil || log == nil {
		return nil, errors.New("HTTP: auth service and logger are required")
	}
	if cfg.Production {
		if !cfg.CookieSecure {
			return nil, errors.New("HTTP: production requires secure cookies")
		}
		for _, origin := range cfg.AllowedOrigins {
			if !strings.HasPrefix(origin, "https://") {
				return nil, errors.New("HTTP: production requires HTTPS origins")
			}
		}
	}
	origins, err := middleware.NewOriginPolicy(cfg.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	cookies := handlers.CookieConfig{Secure: cfg.CookieSecure}
	h := handlers.NewAuth(auth, cookies)
	requireAuth := middleware.RequireAuth(auth, log, cookies.SessionName())
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/auth/login", middleware.LoginRateLimit(log)(origins.RequireOrigin(log)(http.HandlerFunc(h.Login))))
	mux.Handle("GET /api/v1/auth/me", requireAuth(http.HandlerFunc(h.Me)))
	mux.Handle("POST /api/v1/auth/logout", middleware.OptionalAuth(auth, log, cookies.SessionName())(origins.LogoutCSRF(log)(http.HandlerFunc(h.Logout))))
	// Diagnostic RBAC endpoint; remove when real admin endpoints replace it.
	mux.Handle("GET /api/v1/admin/ping", requireAuth(middleware.RequireRole(log, domain.RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	}))))
	protected := origins.CORS(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if cfg.Production && r.TLS == nil {
			middleware.JSONError(w, http.StatusBadRequest, "HTTPS required")
			return
		}
		if cfg.Production {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		protected.ServeHTTP(w, r)
	}), nil
}
