package aiclient

import (
	"ai-assistant/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProfileTransportAndReportIdentity(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			profile := json.RawMessage(`{"profile_version":2,"profile_id":"sample","revision":"2.0.1","rules_version":"r1","engine_version":"docx-profile/2.0.0"}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Service-Token") != "secret" {
					t.Error("service auth lost")
				}
				if err := r.ParseMultipartForm(6 << 20); err != nil {
					t.Error(err)
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("schema") != string(profile) {
					t.Error("profile changed during transport")
				}
				revision := "2.0.1"
				if stale {
					revision = "2.0.0"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(TemplateInspection{CanRender: true, Validation: &domain.TemplateValidation{Ready: true, ProfileVersion: 2, ProfileID: "sample", ProfileRevision: revision, RulesVersion: "r1", EngineVersion: "docx-profile/2.0.0"}})
			}))
			defer server.Close()
			client, err := New(Config{URL: server.URL, Token: "secret", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.InspectProfile(context.Background(), []byte("source"), profile, nil)
			if stale && !errors.Is(err, domain.ErrTemplateEngine) || !stale && err != nil {
				t.Fatal("wrong report accepted", err)
			}
		})
	}
}
