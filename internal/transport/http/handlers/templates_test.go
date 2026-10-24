package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

func TestTemplateErrorMapping(t *testing.T) {
	h := NewTemplates(nil, zap.NewNop())
	for _, test := range []struct {
		err    error
		status int
	}{{domain.ErrRevisionConflict, 409}, {domain.ErrNotFound, 404}, {domain.ErrTemplateBusy, 503}, {domain.ErrTemplateEngine, 502}, {context.DeadlineExceeded, 502}, {domain.ErrTemplateLimit, 413}, {domain.ErrGenerationCommitUnknown, 503}, {domain.TemplateValidation{Ready: false}, 422}} {
		w := httptest.NewRecorder()
		h.fail(w, test.err)
		if w.Code != test.status {
			t.Fatalf("%v: got %d", test.err, w.Code)
		}
	}
}
func TestTemplateRequestBoundary(t *testing.T) {
	for _, body := range []string{`{"unknown":true}`, `{} {}`, `{"name":"` + strings.Repeat("x", 256<<10) + `"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var target struct {
			Name string `json:"name"`
		}
		if readTemplateJSON(w, r, &target) == nil {
			t.Fatal("unbounded or unknown JSON accepted")
		}
	}
}
