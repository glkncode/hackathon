package middleware

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

type bucket struct {
	start time.Time
	count int
}

// LoginRateLimit bounds attempts by direct peer IP (10/15min) and globally
// (100/15min). State is bounded to 4096 peers and local to this process.
func LoginRateLimit(log *zap.Logger) func(http.Handler) http.Handler {
	var mu sync.Mutex
	peers := make(map[string]bucket)
	var global bucket
	const window = 15 * time.Minute
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}
			// X-Forwarded-For is deliberately ignored.
			now := time.Now()
			mu.Lock()
			if now.Sub(global.start) >= window {
				global = bucket{start: now}
			}
			b, exists := peers[peer]
			if !exists && len(peers) >= 4096 {
				for key, value := range peers {
					if now.Sub(value.start) >= window {
						delete(peers, key)
					}
				}
			}
			if now.Sub(b.start) >= window {
				b = bucket{start: now}
			}
			allowed := b.count < 10 && global.count < 100 && (exists || len(peers) < 4096)
			if allowed {
				b.count++
				global.count++
				peers[peer] = b
			}
			retry := int(window.Seconds())
			if !b.start.IsZero() {
				retry = max(1, int(time.Until(b.start.Add(window)).Seconds())+1)
			}
			mu.Unlock()
			if !allowed {
				log.Warn("access denied", zap.String("reason", "login rate limit"))
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				JSONError(w, http.StatusTooManyRequests, "too many attempts")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
