package aiclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRemoteFailureLabelsNeverIncludeUntrustedText(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"code":"resolver_invalid_response"}}`, "resolver_invalid_response"},
		{`{"error":{"code":"PRIVATE DOCUMENT"}}`, "ai_rejected"},
		{`{"error":{"code":"llm_timeout","code":"PRIVATE DOCUMENT"}}`, "ai_rejected"},
		{strings.Repeat("secret", 1000), "ai_rejected"},
	} {
		response := &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(tc.body))}
		err := rejectedResponse(response)
		if err.Code != "ai_rejected" || FailureCode(err) != tc.want || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("remote body became log label")
		}
	}
}
