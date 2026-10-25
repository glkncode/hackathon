package aiclient

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func knownFailureCode(code string) bool {
	switch code {
	case "llm_timeout", "llm_unavailable", "llm_upstream_error", "llm_invalid_response",
		"resolver_timeout", "resolver_invalid_response", "resolver_clarification_rejected", "invalid_rag_request",
		"evidence_invalid_response", "embedding_busy", "embedding_timeout", "embedding_unavailable", "embedding_input_limit",
		"ai_timeout", "ai_unavailable", "ai_rejected", "invalid_ai_response", "invalid_resolver_request", "invalid_resolver_response":
		return true
	}
	return false
}

func rejectedResponse(resp *http.Response) *Error {
	result := &Error{Code: "ai_rejected", Temporary: resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(data) > 4096 || exactJSON(data) != nil {
		return result
	}
	var body struct {
		Error struct {
			Code     string `json:"code"`
			LLMCalls int    `json:"llm_calls"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && knownFailureCode(body.Error.Code) {
		result.UpstreamCode = body.Error.Code
		if body.Error.LLMCalls >= 1 && body.Error.LLMCalls <= 2 {
			result.LLMCalls = body.Error.LLMCalls
		}
	}
	return result
}

// FailureCode is suitable for metrics/log labels. Error strings and arbitrary
// upstream fields can include private documents and must never become labels.
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	var remote *Error
	if errors.As(err, &remote) {
		if knownFailureCode(remote.UpstreamCode) {
			return remote.UpstreamCode
		}
		if knownFailureCode(remote.Code) {
			return remote.Code
		}
	}
	return "invalid_response"
}
