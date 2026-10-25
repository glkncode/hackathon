package postgres

import (
	"context"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Replay the previously accepted Resolver decision, with real SQL visibility
// and persistence. No inference or dependence on the model's current output.
type noCandidatesAI struct {
	t         *testing.T
	canonical bool
}

func (a noCandidatesAI) Resolve(context.Context, aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
	q := "Уточните, пожалуйста, что именно подразумевается под 'телескопом Меридиан': это астрономический инструмент, программное обеспечение, название организации или другой объект?"
	if a.canonical {
		q = aiclient.MissingSubjectQuestion
		return aiclient.ResolveResponse{Status: "needs_clarification", ClarificationKind: "missing_subject", Queries: [][]aiclient.QueryPart{}, ClarificationQuestion: &q, LLMCalls: 1}, nil
	}
	return aiclient.ResolveResponse{Status: "needs_clarification", Queries: [][]aiclient.QueryPart{}, ClarificationQuestion: &q, LLMCalls: 1}, nil
}
func (a noCandidatesAI) Generate(context.Context, aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
	a.t.Fatal("generator called without evidence")
	return aiclient.GenerateResponse{}, nil
}
func (a noCandidatesAI) Embed(_ context.Context, kind string, texts []string) (aiclient.EmbeddingResponse, error) {
	v := make([]float64, 384)
	v[0] = 1
	return aiclient.EmbeddingResponse{Model: domain.EmbeddingModel, Revision: domain.EmbeddingRevision,
		Format: domain.EmbeddingFormat, Dimensions: 384, Embeddings: [][]float64{v},
		Windows: []domain.EmbeddingWindow{{InputIndex: 0, WindowIndex: 0, StartToken: 0, EndToken: 2, TotalTokens: 2}}}, nil
}
func (a noCandidatesAI) Evidence(context.Context, string, []domain.SearchResult) ([]uuid.UUID, error) {
	a.t.Fatal("Gate called without authorized candidates")
	return nil, nil
}

func TestIntegrationChatNoCandidatesRejectsUnfoundedClarification(t *testing.T) {
	for _, corpus := range []string{"empty", "ACL_denied", "valid_plan_empty", "valid_plan_ACL_denied"} {
		t.Run(corpus, func(t *testing.T) {
			f := newSearchFixture(t)
			if corpus == "ACL_denied" || corpus == "valid_plan_ACL_denied" {
				f.document(t, "Private equipment", domain.VersionReady, []domain.DocumentChunk{{Content: "private-only-token"}})
			}
			ai := noCandidatesAI{t: t, canonical: corpus == "valid_plan_empty" || corpus == "valid_plan_ACL_denied"}
			search := service.NewSearchService(f.repo)
			search.EnableSemantic(NewSemanticSearchRepository(f.db), ai, ai, zap.NewNop(), false, time.Second)
			chat, err := service.NewChatServiceWithRAG(NewChatRepository(f.db), search, ai, zap.NewNop(), service.RAGConfig{
				Timeout: time.Second, MaxSources: 8, MaxHistoryMessages: 10, ContextResolverEnabled: true, ContextResolverTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			session, err := chat.CreateSession(f.ctx, f.memberA, "no candidates")
			if err != nil {
				t.Fatal(err)
			}
			turn, err := chat.SendMessage(f.ctx, f.memberA, session.ID, uuid.New(), "Как получить телескоп Меридиан?")
			if err != nil {
				t.Fatal("unexpected technical error", err)
			}
			t.Logf("actual mode=%v content=%q sources=%d", turn.AssistantMessage.Mode, turn.AssistantMessage.Content, len(turn.AssistantMessage.Sources))
			if turn.AssistantMessage.Mode == nil || *turn.AssistantMessage.Mode != domain.ChatModeNoSources || len(turn.AssistantMessage.Sources) != 0 {
				t.Fatalf("no-candidates handling: expected no_sources; actual=%+v", turn.AssistantMessage)
			}
		})
	}
}
