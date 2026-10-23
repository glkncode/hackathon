package domain

import (
	"time"

	"github.com/google/uuid"
)

// Session stores token hashes only. Expiry, revocation and user activity are
// checked by a future authentication service, not by repository lookups.
type Session struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	TokenHash     []byte     `json:"-"`
	CSRFTokenHash []byte     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}
