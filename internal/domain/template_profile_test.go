package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedProfileEnvelopesAndValueTypes(t *testing.T) {
	paths, err := filepath.Glob("../../docs/template-profiles/v2/*.json")
	if err != nil || len(paths) != 7 {
		t.Fatal(paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := ProfileFields(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(ValidateProfileValueTypes(fields, map[string]json.RawMessage{"unknown": json.RawMessage(`"x"`)}).InvalidFields) != 1 {
				t.Fatal("unknown field accepted")
			}
			for _, f := range fields {
				if f.Type == "choice" {
					if ValidateProfileValueTypes([]TemplateField{f}, map[string]json.RawMessage{f.Key: json.RawMessage(`"invalid"`)}).Ready {
						t.Fatal("choice accepted")
					}
				}
				if f.Type == "repeater" {
					for _, data := range []string{`[{}]`, `"bad"`, `[{"name":"x","unknown":"x"}]`} {
						if ValidateProfileValueTypes([]TemplateField{f}, map[string]json.RawMessage{f.Key: json.RawMessage(data)}).Ready {
							t.Fatal("invalid row accepted")
						}
					}
				}
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `{"profile_version":3,"fields":[]}`, `{"profile_version":2,"fields":[]}`} {
		if _, err := ProfileFields(json.RawMessage(raw)); err == nil {
			t.Fatal("bad envelope accepted")
		}
	}
}
