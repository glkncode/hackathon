package domain

import (
	"time"

	"github.com/google/uuid"
)

type IngestionJobStatus string

const (
	JobPending    IngestionJobStatus = "pending"
	JobProcessing IngestionJobStatus = "processing"
	JobCompleted  IngestionJobStatus = "completed"
	JobFailed     IngestionJobStatus = "failed"
)

// LastError is a safe machine-readable code, never a remote response or raw error.
type IngestionJob struct {
	ID                uuid.UUID          `json:"id"`
	DocumentVersionID uuid.UUID          `json:"document_version_id"`
	Status            IngestionJobStatus `json:"status"`
	Attempts          int                `json:"attempts"`
	MaxAttempts       int                `json:"-"`
	NextAttemptAt     *time.Time         `json:"-"`
	LeaseUntil        *time.Time         `json:"-"`
	LeaseToken        uuid.NullUUID      `json:"-"`
	LastError         string             `json:"last_error,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"-"`
	CompletedAt       *time.Time         `json:"completed_at"`
}
