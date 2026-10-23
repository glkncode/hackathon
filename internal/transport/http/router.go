package httptransport

import (
	"errors"
	"net/http"
	"strings"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
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

type Modules struct {
	Ingestion      *service.IngestionService
	Users          *service.UserService
	Documents      *service.DocumentService
	Access         *service.DocumentAccessService
	MaxUploadBytes int64
}

func NewRouter(auth Auth, log *zap.Logger, cfg RouterConfig, modules ...Modules) (http.Handler, error) {
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
	if len(modules) > 1 {
		return nil, errors.New("HTTP: supply at most one module set")
	}
	if len(modules) == 1 {
		m := modules[0]
		if m.Users == nil || m.Documents == nil || m.Access == nil || m.MaxUploadBytes < 1 || m.MaxUploadBytes > 1<<30 {
			return nil, errors.New("HTTP: invalid document/user modules")
		}
		users := handlers.NewUsers(m.Users, log)
		documents := handlers.NewDocuments(m.Documents, m.Access, m.MaxUploadBytes, log)
		admin := middleware.RequireRole(log, domain.RoleAdmin)
		csrf := origins.CSRF(log)
		if m.Ingestion != nil {
			ingestion := handlers.NewIngestion(m.Ingestion, log)
			mux.Handle("GET /api/v1/ingestion-jobs/{id}", requireAuth(admin(http.HandlerFunc(ingestion.Get))))
			mux.Handle("POST /api/v1/ingestion-jobs/{id}/retry", requireAuth(admin(csrf(http.HandlerFunc(ingestion.Retry)))))
		}
		mux.Handle("POST /api/v1/users", requireAuth(admin(csrf(http.HandlerFunc(users.Create)))))
		mux.Handle("GET /api/v1/users", requireAuth(admin(http.HandlerFunc(users.List))))
		mux.Handle("POST /api/v1/documents", requireAuth(admin(csrf(http.HandlerFunc(documents.Upload)))))
		mux.Handle("GET /api/v1/documents", requireAuth(http.HandlerFunc(documents.List)))
		mux.Handle("GET /api/v1/documents/{id}", requireAuth(http.HandlerFunc(documents.Get)))
		mux.Handle("GET /api/v1/documents/{id}/download", requireAuth(http.HandlerFunc(documents.Download)))
		mux.Handle("POST /api/v1/documents/{id}/versions", requireAuth(admin(csrf(http.HandlerFunc(documents.UploadVersion)))))
		mux.Handle("GET /api/v1/documents/{id}/versions", requireAuth(http.HandlerFunc(documents.ListVersions)))
		mux.Handle("GET /api/v1/documents/{id}/versions/{versionId}", requireAuth(http.HandlerFunc(documents.GetVersion)))
		mux.Handle("GET /api/v1/documents/{id}/versions/{versionId}/download", requireAuth(http.HandlerFunc(documents.DownloadVersion)))
		mux.Handle("GET /api/v1/documents/{id}/access", requireAuth(admin(http.HandlerFunc(documents.ListAccess))))
		mux.Handle("POST /api/v1/documents/{id}/access", requireAuth(admin(csrf(http.HandlerFunc(documents.Grant)))))
		mux.Handle("DELETE /api/v1/documents/{id}/access/{userId}", requireAuth(admin(csrf(http.HandlerFunc(documents.Revoke)))))
	}
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
