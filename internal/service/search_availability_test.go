package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

func TestDegradedEmptySearchDoesNotClaimMissingInformation(t *testing.T) {
	for _, stage := range []string{"embedding", "evidence", "empty_index", "negative_evidence"} {
		t.Run(stage, func(t *testing.T) {
			source := ragSource()
			sem := &semanticStub{response: goodEmbedding(), results: []domain.SearchResult{source}}
			switch stage {
			case "embedding":
				sem.embedErr = errors.New("offline")
			case "evidence":
				sem.gateErr = errors.New("offline")
			case "empty_index":
				sem.results = nil
			}
			search := NewSearchService(&searchStoreStub{})
			search.EnableSemantic(sem, sem, sem, zap.NewNop(), false, time.Second)
			results, err := search.Search(context.Background(), searchActor(), domain.SearchQuery{Query: "question"})
			if stage == "embedding" || stage == "evidence" {
				if !errors.Is(err, domain.ErrSearchUnavailable) {
					t.Fatal("technical failure became no_sources", err)
				}
			} else if err != nil || len(results) != 0 {
				t.Fatal("genuine empty result changed", err)
			}
		})
	}
}
