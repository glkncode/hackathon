package config

import (
	"testing"
	"time"
)

func TestSemanticDefaultsAndValidation(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://localhost/config_test"}
	cfg, err := load(func(k string) string { return base[k] })
	if err != nil || cfg.SemanticSearchEnabled || cfg.SemanticSupplementFTS || cfg.EmbeddingDimensions != 384 || cfg.EmbeddingRequestTimeout != 30*time.Second {
		t.Fatal("semantic defaults", err)
	}
	for _, settings := range []map[string]string{
		{"SEMANTIC_SEARCH_ENABLED": "true"},
		{"SEMANTIC_INDEXING_ENABLED": "true"},
		{"SEMANTIC_INDEXING_ENABLED": "maybe"},
		{"EMBEDDING_DIMENSIONS": "768"},
		{"EMBEDDING_MODEL": "arbitrary/client-model"},
		{"EMBEDDING_REQUEST_TIMEOUT": "2h"},
		{"SEMANTIC_SEARCH_TIMEOUT": "0s"},
		{"SEMANTIC_SUPPLEMENT_FTS": "maybe"},
	} {
		config, err := load(func(k string) string {
			if v, ok := settings[k]; ok {
				return v
			}
			return base[k]
		})
		if err == nil {
			err = config.ValidateServer()
		}
		if err == nil {
			t.Fatalf("invalid semantic configuration accepted: %v", settings)
		}
	}
}

func TestSemanticIndexingIndependentOfSearch(t *testing.T) {
	for _, tc := range []struct {
		search, indexing string
		want             bool
	}{
		{"", "", false}, {"true", "", true}, {"false", "true", true}, {"true", "false", false},
	} {
		cfg, err := load(func(k string) string {
			switch k {
			case "DATABASE_URL":
				return "postgres://localhost/config_test"
			case "SEMANTIC_SEARCH_ENABLED":
				return tc.search
			case "SEMANTIC_INDEXING_ENABLED":
				return tc.indexing
			}
			return ""
		})
		if err != nil || cfg.SemanticIndexingEnabled != tc.want {
			t.Fatalf("%+v: %+v %v", tc, cfg, err)
		}
	}
}
