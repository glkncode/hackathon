package domain

import (
	"github.com/google/uuid"
	"time"
)

// Values live exclusively in TemplateDraft. Version protects conversation state;
// ReviewedRevision binds confirmation to the exact values shown to the user.
type ChatDocumentWorkflow struct {
	SessionID        uuid.UUID   `json:"session_id"`
	OwnerID          uuid.UUID   `json:"-"`
	DraftID          *uuid.UUID  `json:"draft_id,omitempty"`
	Stage            string      `json:"stage"`
	Version          int64       `json:"version"`
	ReviewedRevision int64       `json:"reviewed_revision"`
	ExpectedField    string      `json:"expected_field,omitempty"`
	CandidateIDs     []uuid.UUID `json:"-"`
	GeneratedID      *uuid.UUID  `json:"generated_id,omitempty"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

type ChatTemplateOption struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
}

type ChatDocumentView struct {
	Enabled      bool                  `json:"enabled"`
	Workflow     *ChatDocumentWorkflow `json:"workflow,omitempty"`
	Draft        *TemplateDraft        `json:"draft,omitempty"`
	TemplateName string                `json:"template_name,omitempty"`
	Options      []ChatTemplateOption  `json:"options,omitempty"`
	Generated    *GeneratedDocument    `json:"generated,omitempty"`
	Unavailable  bool                  `json:"unavailable,omitempty"`
}
