package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

type OriginPolicy struct{ allowed map[string]bool }

func NewOriginPolicy(origins []string) (*OriginPolicy, error) {
	if len(origins) == 0 {
		return nil, errors.New("HTTP: trusted origins are required")
	}
	p := &OriginPolicy{allowed: make(map[string]bool)}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || origin != u.Scheme+"://"+u.Host {
			return nil, errors.New("HTTP: invalid trusted origin")
		}
		p.allowed[origin] = true
	}
	return p, nil
}

func (p *OriginPolicy) permits(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return len(values) == 1 && p.allowed[values[0]]
}

func (p *OriginPolicy) RequireOrigin(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !p.permits(r) {
				log.Warn("access denied", zap.String("reason", "origin"))
				JSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (p *OriginPolicy) CSRF(log *zap.Logger) func(http.Handler) http.Handler {
	return p.csrf(log, false)
}

// LogoutCSRF requires CSRF for active sessions. A missing/already invalid
// session permits only cookie clearing, still requiring a trusted Origin.
func (p *OriginPolicy) LogoutCSRF(log *zap.Logger) func(http.Handler) http.Handler {
	return p.csrf(log, true)
}

func (p *OriginPolicy) csrf(log *zap.Logger, anonymousLogout bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			deny := func() {
				log.Warn("access denied", zap.String("reason", "csrf"))
				JSONError(w, http.StatusForbidden, "forbidden")
			}
			if !p.permits(r) {
				deny()
				return
			}
			identity, ok := Current(r.Context())
			if !ok {
				if anonymousLogout {
					next.ServeHTTP(w, r)
				} else {
					deny()
				}
				return
			}
			values := r.Header.Values("X-CSRF-Token")
			if len(values) != 1 {
				deny()
				return
			}
			hash, err := service.HashToken(values[0])
			if err != nil || len(identity.Session.CSRFTokenHash) != 32 || subtle.ConstantTimeCompare(hash, identity.Session.CSRFTokenHash) != 1 {
				deny()
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows credentials only for exact trusted origins. It never trusts
// forwarded headers and never emits Access-Control-Allow-Origin: *.
func (p *OriginPolicy) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") != "" {
			if !p.permits(r) {
				JSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			method := r.Header.Get("Access-Control-Request-Method")
			if !p.permits(r) || (method != "GET" && method != "POST" && method != "DELETE" && method != "PATCH") {
				JSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			for _, name := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				name = strings.ToLower(strings.TrimSpace(name))
				if name != "" && name != "content-type" && name != "x-csrf-token" {
					JSONError(w, http.StatusForbidden, "forbidden")
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
