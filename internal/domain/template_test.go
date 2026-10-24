package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTemplateValidation(t *testing.T) {
	fields := []TemplateField{{Key: "name", Label: "Имя", Type: "person", Required: true, Question: "Кто?", MaxLength: 20}, {Key: "date", Label: "Дата", Type: "date", Required: true, Question: "Когда?", MaxLength: 10}, {Key: "members", Label: "Члены", Type: "person_list", Required: true, Question: "Кто входит?", MaxLength: 20}}
	if err := ValidateTemplateSchema(fields); err != nil {
		t.Fatal(err)
	}
	raw := func(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
	values := map[string]json.RawMessage{}
	v := ValidateTemplateValues(fields, values)
	if v.Ready || v.NextQuestion.Key != "name" || len(v.MissingFields) != 3 {
		t.Fatal(v)
	}
	values["name"] = raw("   ")
	if len(ValidateTemplateValues(fields, values).MissingFields) != 3 {
		t.Fatal("spaces accepted")
	}
	for _, bad := range []any{42, strings.Repeat("я", 21), "\x00"} {
		values["name"] = raw(bad)
		if len(ValidateTemplateValues(fields, values).InvalidFields) == 0 {
			t.Fatal("bad name accepted")
		}
	}
	values["name"] = raw("Иванов")
	if ValidateTemplateValues(fields, values).NextQuestion.Key != "date" {
		t.Fatal("wrong next question")
	}
	values["date"] = raw("2026-02-30")
	if len(ValidateTemplateValues(fields, values).InvalidFields) != 1 {
		t.Fatal("bad date accepted")
	}
	values["date"] = raw("2026-12-15")
	values["members"] = raw([]string{})
	if ValidateTemplateValues(fields, values).Ready {
		t.Fatal("empty required list accepted")
	}
	values["members"] = raw([]string{"Сидоров", "Кузнецова"})
	if !ValidateTemplateValues(fields, values).Ready {
		t.Fatal("valid values rejected")
	}
	values["unknown"] = raw("x")
	if ValidateTemplateValues(fields, values).Ready {
		t.Fatal("unknown key accepted")
	}
}
func TestTemplateSchemaLimits(t *testing.T) {
	for _, fields := range [][]TemplateField{nil, {{Key: "bad.key", Label: "x", Question: "x", Type: "text"}}, {{Key: "x", Label: "x", Question: "x", Type: "code"}}, {{Key: "x", Label: "x", Question: "x", Type: "text", Bindings: []TemplateBinding{{Part: "../../x", Match: "x"}}}}} {
		if ValidateTemplateSchema(fields) == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
