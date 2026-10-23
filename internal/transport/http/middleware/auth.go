package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

type Authenticator interface {
	Authenticate(context.Context, string) (service.Identity, error)
}
type identityKey struct{}

func Current(ctx context.Context) (service.Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(service.Identity)
	return identity, ok
}

func JSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func RequireAuth(auth Authenticator, log *zap.Logger, cookieName string) func(http.Handler) http.Handler {
	return authenticate(auth, log, cookieName, false)
}

// OptionalAuth is reserved for idempotent logout. Invalid/missing sessions do
// not gain an identity; database failures still fail closed with HTTP 500.
func OptionalAuth(auth Authenticator, log *zap.Logger, cookieName string) func(http.Handler) http.Handler {
	return authenticate(auth, log, cookieName, true)
}

func authenticate(auth Authenticator, log *zap.Logger, cookieName string, optional bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var token string
			count := 0
			for _, cookie := range r.Cookies() {
				if cookie.Name == cookieName {
					token = cookie.Value
					count++
				}
			}
			if count != 1 || token == "" {
				if optional {
					next.ServeHTTP(w, r)
					return
				}
				log.Warn("access denied", zap.String("reason", "unauthorized"))
				JSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			identity, err := auth.Authenticate(r.Context(), token)
			if err != nil {
				if errors.Is(err, service.ErrUnauthorized) {
					if optional {
						next.ServeHTTP(w, r)
						return
					}
					log.Warn("access denied", zap.String("reason", "unauthorized"))
					JSONError(w, http.StatusUnauthorized, "unauthorized")
				} else {
					JSONError(w, http.StatusInternalServerError, "internal error")
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
		})
	}
}
