package config

import (
	"testing"
	"time"
)

func TestContextResolverRequiresSemanticRAGAndBoundedTimeout(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://localhost/context_test", "AI_SERVICE_URL": "http://localhost:8000", "AI_SERVICE_TOKEN": "private", "RAG_ENABLED": "true", "SEMANTIC_SEARCH_ENABLED": "true"}
	for _, tc := range []struct {
		flag, timeout, rag, semantic string
		valid                        bool
	}{
		{"false", "8s", "true", "true", true}, {"true", "8s", "true", "true", true},
		{"true", "8s", "false", "true", false}, {"true", "8s", "true", "false", false},
		{"true", "31s", "true", "true", false}, {"true", "1ms", "true", "true", false},
		{"maybe", "8s", "true", "true", false},
	} {
		cfg, err := load(func(k string) string {
			switch k {
			case "CONTEXT_RESOLVER_ENABLED":
				return tc.flag
			case "CONTEXT_RESOLVER_TIMEOUT":
				return tc.timeout
			case "RAG_ENABLED":
				return tc.rag
			case "SEMANTIC_SEARCH_ENABLED":
				return tc.semantic
			}
			return base[k]
		})
		if err == nil {
			err = cfg.ValidateServer()
		}
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	cfg, err := load(func(k string) string { return base[k] })
	if err != nil || cfg.ContextResolverEnabled || cfg.ContextResolverTimeout != 8*time.Second {
		t.Fatal("resolver defaults", err)
	}
}
