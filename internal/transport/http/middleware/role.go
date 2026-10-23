package middleware

import (
	"net/http"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

func RequireRole(log *zap.Logger, roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := Current(r.Context())
			if !ok {
				JSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			for _, role := range roles {
				if identity.User.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			log.Warn("access denied", zap.String("reason", "role"), zap.String("user_id", identity.User.ID.String()))
			JSONError(w, http.StatusForbidden, "forbidden")
		})
	}
}
