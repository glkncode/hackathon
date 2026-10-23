package domain

import (
	"time"

	"github.com/google/uuid"
)

type VersionStatus string

const (
	VersionUploaded   VersionStatus = "uploaded"
	VersionProcessing VersionStatus = "processing"
	VersionReady      VersionStatus = "ready"
	VersionFailed     VersionStatus = "failed"
)

func (s VersionStatus) Valid() bool {
	switch s {
	case VersionUploaded, VersionProcessing, VersionReady, VersionFailed:
		return true
	}
	return false
}

type DocumentVersion struct {
	ID               uuid.UUID     `json:"id"`
	DocumentID       uuid.UUID     `json:"document_id"`
	VersionNumber    int           `json:"version_number"`
	StorageKey       string        `json:"-"`
	OriginalFilename string        `json:"original_filename"`
	MIMEType         string        `json:"mime_type"`
	SizeBytes        int64         `json:"size_bytes"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	Status           VersionStatus `json:"status"`
	SHA256           string        `json:"sha256,omitempty"` // Absent for legacy originals not checksummed yet.
	IngestionJobID   uuid.NullUUID `json:"ingestion_job_id"`
}
