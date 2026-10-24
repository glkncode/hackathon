package domain

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const DOCXMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
const MaxTemplateBytes = 5 << 20
const MaxGeneratedBytes = 10 << 20

var ErrRevisionConflict = errors.New("draft revision conflict")
var ErrTemplateEngine = errors.New("template engine unavailable")
var ErrTemplateBusy = errors.New("template generation capacity reached")
var ErrTemplateLimit = errors.New("template resource limit exceeded")
var ErrTemplateNeedsConfiguration = errors.New("template needs configuration")

// A failed COMMIT acknowledgement does not prove that PostgreSQL rolled back.
// Keep the private file so a successful but unacknowledged commit stays readable.
var ErrGenerationCommitUnknown = errors.New("generation commit outcome unknown")

type TemplateBinding struct {
	Part      string `json:"part"`
	Paragraph int    `json:"paragraph"`
	Match     string `json:"match"`
	Suffix    string `json:"suffix,omitempty"`
}
type TemplateField struct {
	Key          string              `json:"key"`
	Label        string              `json:"label"`
	Type         string              `json:"type"`
	Required     bool                `json:"required"`
	Question     string              `json:"question"`
	Description  string              `json:"description"`
	MaxLength    int                 `json:"max_length"`
	Bindings     []TemplateBinding   `json:"bindings,omitempty"`
	RequiredIf   json.RawMessage     `json:"required_if,omitempty"`
	VisibleIf    json.RawMessage     `json:"visible_if,omitempty"`
	Options      []string            `json:"options,omitempty"`
	ItemFields   []TemplateItemField `json:"item_fields,omitempty"`
	MinimumItems int                 `json:"minimum_items,omitempty"`
	MaximumItems int                 `json:"maximum_items,omitempty"`
	Source       json.RawMessage     `json:"source,omitempty"`
}
type TemplateItemField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	MaxLength int    `json:"max_length,omitempty"`
}
type DocumentTemplate struct {
	ID                uuid.UUID       `json:"id"`
	DocumentID        uuid.UUID       `json:"document_id"`
	DocumentVersionID uuid.UUID       `json:"document_version_id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Enabled           bool            `json:"enabled"`
	Fields            []TemplateField `json:"fields"`
	SchemaVersion     int             `json:"schema_version"`
	Profile           json.RawMessage `json:"profile,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}
type TemplateDraft struct {
	ID                  uuid.UUID                  `json:"id"`
	TemplateID          uuid.UUID                  `json:"template_id"`
	DocumentVersionID   uuid.UUID                  `json:"document_version_id"`
	OwnerID             uuid.UUID                  `json:"owner_id"`
	Values              map[string]json.RawMessage `json:"values"`
	Status              string                     `json:"status"`
	Revision            int64                      `json:"revision"`
	Fields              []TemplateField            `json:"fields"`
	SchemaVersion       int                        `json:"schema_version"`
	Profile             json.RawMessage            `json:"profile,omitempty"`
	Validation          *TemplateValidation        `json:"validation,omitempty"`
	RequiredFieldCount  int                        `json:"required_field_count"`
	CompletedFieldCount int                        `json:"completed_field_count"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
}
type GeneratedDocument struct {
	ID                 uuid.UUID       `json:"id"`
	DraftID            uuid.UUID       `json:"draft_id"`
	OwnerID            uuid.UUID       `json:"owner_id"`
	TemplateID         uuid.UUID       `json:"template_id"`
	DocumentVersionID  uuid.UUID       `json:"document_version_id"`
	DraftRevision      int64           `json:"draft_revision"`
	StorageKey         string          `json:"-"`
	Filename           string          `json:"filename"`
	MIMEType           string          `json:"mime_type"`
	SizeBytes          int64           `json:"size_bytes"`
	SHA256             string          `json:"sha256"`
	GenerationSnapshot json.RawMessage `json:"generation_snapshot,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}
type InvalidTemplateField struct {
	Key  string `json:"key"`
	Code string `json:"code"`
}
type TemplateValidation struct {
	Ready           bool                   `json:"ready"`
	MissingFields   []TemplateField        `json:"missing_fields"`
	InvalidFields   []InvalidTemplateField `json:"invalid_fields"`
	NextQuestion    *TemplateField         `json:"next_question"`
	VisibleFields   []string               `json:"visible_fields,omitempty"`
	RequiredFields  []string               `json:"required_fields,omitempty"`
	Checks          []TemplateCheck        `json:"checks,omitempty"`
	ApprovalRoute   json.RawMessage        `json:"approval_route,omitempty"`
	ProfileVersion  int                    `json:"profile_version,omitempty"`
	ProfileID       string                 `json:"profile_id,omitempty"`
	ProfileRevision string                 `json:"profile_revision,omitempty"`
	RulesVersion    string                 `json:"rules_version,omitempty"`
	EngineVersion   string                 `json:"engine_version,omitempty"`
}
type TemplateCheck struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category"`
	Source   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Section string `json:"section"`
	} `json:"source"`
	Description string `json:"description"`
	Result      string `json:"result"`
	Method      string `json:"method"`
}

func (v TemplateValidation) Error() string { return "template fields are incomplete or invalid" }

func ValidateTemplateSchema(fields []TemplateField) error {
	if len(fields) < 1 || len(fields) > 64 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	keyPattern := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	partPattern := regexp.MustCompile(`^word/(document|header[0-9]+|footer[0-9]+|footnotes)\.xml$`)
	for i := range fields {
		f := &fields[i]
		if f.MaxLength == 0 {
			f.MaxLength = 8000
		}
		if !keyPattern.MatchString(f.Key) || seen[f.Key] || !templateText(f.Label, 255) || strings.TrimSpace(f.Label) == "" || !templateText(f.Question, 1000) || strings.TrimSpace(f.Question) == "" || !templateText(f.Description, 2000) || f.MaxLength < 1 || f.MaxLength > 8000 || len(f.Bindings) > 16 {
			return ErrInvalidInput
		}
		seen[f.Key] = true
		switch f.Type {
		case "text", "multiline_text", "date", "person", "person_list":
		default:
			return ErrInvalidInput
		}
		for j := range f.Bindings {
			b := &f.Bindings[j]
			if b.Part == "" {
				b.Part = "word/document.xml"
			}
			if !partPattern.MatchString(b.Part) || b.Paragraph < 0 || b.Paragraph > 100000 || !templateText(b.Match, 8000) || b.Match == "" || !templateText(b.Suffix, 128) {
				return ErrInvalidInput
			}
		}
	}
	data, err := json.Marshal(fields)
	if err != nil || len(data) > 65536 {
		return ErrInvalidInput
	}
	return nil
}
func templateText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if !(r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF) {
			return false
		}
	}
	return true
}
func ValidateTemplateValues(fields []TemplateField, values map[string]json.RawMessage) TemplateValidation {
	v := TemplateValidation{Ready: true, MissingFields: []TemplateField{}, InvalidFields: []InvalidTemplateField{}}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true
		raw, exists := values[f.Key]
		empty := !exists || string(raw) == "null"
		valid := true
		if !empty {
			if f.Type == "person_list" {
				var list []string
				if json.Unmarshal(raw, &list) != nil {
					valid = false
				} else {
					empty = len(list) == 0
					valid = len(list) <= 50
					for _, s := range list {
						if strings.TrimSpace(s) == "" || !templateText(s, f.MaxLength) {
							valid = false
						}
					}
				}
			} else {
				var s string
				if json.Unmarshal(raw, &s) != nil {
					valid = false
				} else {
					empty = strings.TrimSpace(s) == ""
					valid = templateText(s, f.MaxLength)
					if !empty && f.Type == "date" {
						_, err := time.Parse("2006-01-02", s)
						valid = valid && err == nil && len(s) == 10
					}
				}
			}
		}
		if !valid {
			v.InvalidFields = append(v.InvalidFields, InvalidTemplateField{Key: f.Key, Code: "invalid_value"})
		} else if f.Required && empty {
			v.MissingFields = append(v.MissingFields, f)
		}
	}
	for key := range values {
		if !known[key] {
			v.InvalidFields = append(v.InvalidFields, InvalidTemplateField{Key: key, Code: "unknown_field"})
		}
	}
	v.Ready = len(v.MissingFields) == 0 && len(v.InvalidFields) == 0
	if len(v.MissingFields) > 0 {
		v.NextQuestion = &v.MissingFields[0]
	}
	return v
}
func (d *TemplateDraft) Progress() {
	v := ValidateTemplateValues(d.Fields, d.Values)
	d.RequiredFieldCount = 0
	for _, f := range d.Fields {
		if f.Required {
			d.RequiredFieldCount++
		}
	}
	d.CompletedFieldCount = d.RequiredFieldCount - len(v.MissingFields)
	for _, f := range v.InvalidFields {
		for _, def := range d.Fields {
			if def.Key == f.Key && def.Required {
				d.CompletedFieldCount--
			}
		}
	}
}
