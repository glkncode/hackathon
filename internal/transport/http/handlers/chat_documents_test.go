package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

func TestDocumentActionAllowsRepeaterPayloadAndBoundsBody(t *testing.T) {
	h := NewChat(service.NewChatService(nil, nil, zap.NewNop()), zap.NewNop())
	for _, tc := range []struct {
		name   string
		size   int
		status int
	}{
		// An unauthenticated actor reaches the service only after the larger
		// body is decoded. The old 4 KiB reader returned 400 before this point.
		{"large repeater reaches authorization", 12 << 10, 403},
		{"oversized input rejected", 257 << 10, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"answer","values":{"rows":[{"text":"`+strings.Repeat("x", tc.size)+`"}]}}`))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("id", "01234567-89ab-4def-8123-456789abcdef")
			w := httptest.NewRecorder()
			h.DocumentAction(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
