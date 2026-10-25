package config

import "testing"

func TestChatDocumentsFlagIndependentFromResolver(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://localhost/chat_documents_test", "AI_SERVICE_URL": "http://localhost:8000", "AI_SERVICE_TOKEN": "private"}
	for _, flag := range []string{"", "false", "true", "invalid"} {
		cfg, err := load(func(k string) string {
			if k == "CHAT_DOCUMENT_CREATION_ENABLED" {
				return flag
			}
			return base[k]
		})
		if flag == "invalid" {
			if err == nil {
				t.Fatal("invalid flag accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ChatDocumentCreationEnabled != (flag == "true") || cfg.ContextResolverEnabled {
			t.Fatal("feature depends on resolver or defaults to enabled")
		}
		if err = cfg.ValidateServer(); err != nil {
			t.Fatal(err)
		}
		if flag == "true" {
			cfg.AIServiceToken = ""
			if cfg.ValidateServer() == nil {
				t.Fatal("unconfigured feature enabled")
			}
		}
	}
}
