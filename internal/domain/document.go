package domain

import (
	"time"

	"github.com/google/uuid"
)

type Document struct {
	ID              uuid.UUID     `json:"id"`
	Title           string        `json:"title"`
	CreatedBy       uuid.UUID     `json:"created_by"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	ActiveVersionID uuid.NullUUID `json:"active_version_id"`
}
