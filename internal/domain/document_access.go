package domain

import (
	"time"

	"github.com/google/uuid"
)

type DocumentAccess struct {
	ID         uuid.UUID `json:"id"`
	DocumentID uuid.UUID `json:"document_id"`
	UserID     uuid.UUID `json:"user_id"`
	GrantedBy  uuid.UUID `json:"granted_by"`
	CreatedAt  time.Time `json:"created_at"`
}
