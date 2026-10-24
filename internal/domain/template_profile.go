package domain

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The Python engine validates the complete bounded profile and structural anchors.
// Go validates the transport envelope and field types before accepting values.
func ProfileFields(raw json.RawMessage) ([]TemplateField, error) {
	if len(raw) == 0 || len(raw) > 196608 {
		return nil, ErrInvalidInput
	}
	var envelope struct {
		ProfileVersion int             `json:"profile_version"`
		Fields         []TemplateField `json:"fields"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ProfileVersion != 2 {
		return nil, ErrInvalidInput
	}
	fields := envelope.Fields
	if len(fields) < 1 || len(fields) > 64 {
		return nil, ErrInvalidInput
	}
	seen := map[string]bool{}
	for index, f := range fields {
		if f.MaximumItems == 0 {
			f.MaximumItems = 30
			fields[index].MaximumItems = 30
		}
		if seen[f.Key] {
			return nil, ErrInvalidInput
		}
		seen[f.Key] = true
		base := f
		base.Type = "text"
		base.Bindings = nil
		if ValidateTemplateSchema([]TemplateField{base}) != nil || f.MaxLength < 1 || f.MaxLength > 8000 || len(f.Bindings) > 0 {
			return nil, ErrInvalidInput
		}
		switch f.Type {
		case "text", "multiline_text", "person", "date", "person_list", "text_list":
		case "choice":
			if len(f.Options) < 1 || len(f.Options) > 16 {
				return nil, ErrInvalidInput
			}
		case "repeater":
			if len(f.ItemFields) < 1 || len(f.ItemFields) > 8 || f.MinimumItems < 0 || f.MaximumItems < 1 || f.MaximumItems > 30 || f.MinimumItems > f.MaximumItems {
				return nil, ErrInvalidInput
			}
		default:
			return nil, ErrInvalidInput
		}
	}
	return fields, nil
}

func ValidateProfileValueTypes(fields []TemplateField, values map[string]json.RawMessage) TemplateValidation {
	// Cross-field and semantic rules are evaluated by the pinned Python rules engine.
	v := TemplateValidation{Ready: true, MissingFields: []TemplateField{}, InvalidFields: []InvalidTemplateField{}}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true
		raw, ok := values[f.Key]
		if !ok || bytes.Equal(raw, []byte("null")) {
			continue
		}
		valid := true
		switch f.Type {
		case "choice":
			var s string
			valid = json.Unmarshal(raw, &s) == nil
			found := s == ""
			for _, option := range f.Options {
				if s == option {
					found = true
				}
			}
			valid = valid && found
		case "repeater":
			var rows []map[string]json.RawMessage
			valid = json.Unmarshal(raw, &rows) == nil && len(rows) <= f.MaximumItems && (len(rows) == 0 || len(rows) >= f.MinimumItems)
			if valid {
				for _, row := range rows {
					allowed := map[string]bool{}
					for _, item := range f.ItemFields {
						allowed[item.Key] = true
						data, exists := row[item.Key]
						var s string
						limit := item.MaxLength
						if limit == 0 {
							limit = 1000
						}
						if !exists {
							if item.Required {
								valid = false
							}
							continue
						}
						if json.Unmarshal(data, &s) != nil || !templateText(s, limit) || item.Required && strings.TrimSpace(s) == "" {
							valid = false
						}
					}
					for k := range row {
						if !allowed[k] {
							valid = false
						}
					}
				}
			}
		default:
			base := f
			base.Required = false
			if base.Type == "text_list" {
				base.Type = "person_list"
			}
			valid = len(ValidateTemplateValues([]TemplateField{base}, map[string]json.RawMessage{f.Key: raw}).InvalidFields) == 0
		}
		if !valid {
			v.InvalidFields = append(v.InvalidFields, InvalidTemplateField{Key: f.Key, Code: "invalid_value"})
		}
	}
	for key := range values {
		if !known[key] {
			v.InvalidFields = append(v.InvalidFields, InvalidTemplateField{Key: key, Code: "unknown_field"})
		}
	}
	v.Ready = len(v.InvalidFields) == 0
	return v
}
