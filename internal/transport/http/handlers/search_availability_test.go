package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

func TestSearchUnavailableIsRetryableAndDoesNotClaimNoEvidence(t *testing.T) {
	w := httptest.NewRecorder()
	NewChat(nil, zap.NewNop()).fail(w, domain.ErrSearchUnavailable)
	if w.Code != 503 || strings.Contains(w.Body.String(), "no_sources") {
		t.Fatal("incorrect chat failure", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	apiError(w, domain.ErrSearchUnavailable, zap.NewNop())
	if w.Code != 503 {
		t.Fatal("incorrect search failure", w.Code)
	}
}
