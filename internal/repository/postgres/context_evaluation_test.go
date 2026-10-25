package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type contextEvaluationAI struct {
	*aiclient.Client
	trace map[string]any
}

func (c *contextEvaluationAI) Resolve(ctx context.Context, req aiclient.ResolveRequest) (aiclient.ResolveResponse, error) {
	result, err := c.Client.Resolve(ctx, req)
	queries, _ := result.SearchQueries(req)
	c.trace["resolver"] = map[string]any{"request": req, "result": result, "queries": queries, "error_code": aiclient.FailureCode(err)}
	return result, err
}
func (c *contextEvaluationAI) Generate(ctx context.Context, req aiclient.GenerateRequest) (aiclient.GenerateResponse, error) {
	c.trace["generation_query"] = req.Query
	c.trace["generation_history"] = req.History
	c.trace["generation_sources"] = req.Sources
	result, err := c.Client.Generate(ctx, req)
	c.trace["generation_result"] = result
	c.trace["generation_error_code"] = aiclient.FailureCode(err)
	return result, err
}
func (c *contextEvaluationAI) Evidence(ctx context.Context, q string, sources []domain.SearchResult) ([]uuid.UUID, error) {
	c.trace["gate_query"] = q
	c.trace["gate_candidates"] = sources
	ids, err := c.Client.Evidence(ctx, q, sources)
	c.trace["gate_selected_ids"] = ids
	c.trace["gate_error_code"] = aiclient.FailureCode(err)
	return ids, err
}

// Opt-in model evaluation, separate from deterministic regression tests. The
// corpus is frozen on disk before the first model call. Review actual text and
// cited chunks in observations.json; generated alone is not a correctness score.
func TestIntegrationContextEvaluation(t *testing.T) {
	if os.Getenv("CONTEXT_EVAL_OUT") == "" {
		t.Skip("opt-in real Qwen evaluation")
	}
	type question struct {
		Query, Doc, Expected string
		Facts                []string
		MinSources           int `json:"min_sources"`
		Independent          bool
	}
	var corpus struct {
		Documents []struct {
			Key, Title string
			Chunks     []string
			Private    bool
		}
		Scenarios []struct {
			ID    string
			Turns []question
		}
	}
	corpusPath := os.Getenv("CONTEXT_EVAL_CORPUS")
	if corpusPath == "" {
		corpusPath = "../../../experiments/context-dialogue/corpus.json"
	}
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("CONTEXT_EVAL_OUT")
	if _, err = os.Stat(filepath.Join(out, "observations.json")); err == nil {
		t.Fatal("refusing to overwrite frozen observations")
	}
	if err = os.WriteFile(filepath.Join(out, "corpus.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	f := newSearchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	f.ctx = ctx
	client, err := aiclient.New(aiclient.Config{URL: os.Getenv("CONTEXT_EVAL_AI_URL"), Token: os.Getenv("CONTEXT_EVAL_TOKEN"), Timeout: 60 * time.Second, GenerateTimeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	docs := map[string]uuid.UUID{}
	for _, doc := range corpus.Documents {
		chunks := []domain.DocumentChunk{}
		for i, text := range doc.Chunks {
			chunks = append(chunks, domain.DocumentChunk{ChunkIndex: i, Content: text})
		}
		d, _ := f.document(t, doc.Title, domain.VersionReady, chunks)
		docs[doc.Key] = d.ID
		if !doc.Private {
			f.grant(t, d.ID, f.memberA.ID)
		}
	}
	queue := NewEmbeddingRepository(f.db, domain.E5Identity())
	worker := service.NewSemanticWorker(queue, client, zap.NewNop(), 30*time.Second)
	for range corpus.Documents {
		if err = worker.ProcessNext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	type observation struct {
		Mode, Scenario string
		Step           int
		Question       question
		Turn           domain.ChatTurn
		DurationMS     int64
		Logs           []map[string]any
		Metrics        service.ResolverMetrics
		Trace          map[string]any
		ErrorCode      string
	}
	rows := []observation{}
	controls := []map[string]any{}
	save := func() {
		data, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "observations.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	modes := []string{"baseline", "context"}
	if requested := os.Getenv("CONTEXT_EVAL_MODE"); requested != "" {
		modes = strings.Split(requested, ",")
	}
	for _, mode := range modes {
		if mode != "baseline" && mode != "context" {
			t.Fatal("invalid evaluation mode")
		}
		enabled := mode != "baseline"
		core, logs := observer.New(zap.InfoLevel)
		log := zap.New(core)
		search := service.NewSearchService(f.repo)
		ai := &contextEvaluationAI{Client: client, trace: map[string]any{}}
		search.EnableSemantic(NewSemanticSearchRepository(f.db), client, ai, log, false, 45*time.Second)
		chat, err := service.NewChatServiceWithRAG(NewChatRepository(f.db), search, ai, log, service.RAGConfig{Timeout: 60 * time.Second, MaxSources: 8, MaxHistoryMessages: 10, ContextResolverEnabled: enabled, ContextResolverTimeout: 8 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		for _, scenario := range corpus.Scenarios {
			session, err := chat.CreateSession(ctx, f.memberA, mode+" "+scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			for step, q := range scenario.Turns {
				ai.trace = map[string]any{}
				logs.TakeAll()
				started := time.Now()
				key := uuid.New()
				turn, err := chat.SendMessage(ctx, f.memberA, session.ID, key, q.Query)
				if err != nil && !errors.Is(err, domain.ErrSearchUnavailable) {
					t.Fatal(mode, scenario.ID, step, err)
				}
				errorCode := ""
				if err != nil {
					errorCode = "search_unavailable"
				}
				elapsed := time.Since(started).Milliseconds()
				entries := []map[string]any{}
				for _, entry := range logs.TakeAll() {
					fields := entry.ContextMap()
					fields["event"] = entry.Message
					entries = append(entries, fields)
				}
				for _, source := range turn.AssistantMessage.Sources {
					if source.DocumentID == docs["private"] {
						t.Fatal("ACL leak")
					}
					if source.ChunkID == uuid.Nil || source.VersionNumber != 1 {
						t.Fatal("invalid citation metadata")
					}
				}
				before := chat.ContextMetrics()
				if errorCode == "" {
					replay, replayErr := chat.SendMessage(ctx, f.memberA, session.ID, key, q.Query)
					if replayErr != nil || replay.AssistantMessage.ID != turn.AssistantMessage.ID || before != chat.ContextMetrics() {
						t.Fatal("replay called resolver or lost idempotency", replayErr)
					}
				}
				rows = append(rows, observation{mode, scenario.ID, step + 1, q, turn, elapsed, entries, before, ai.trace, errorCode})
				save()
				if errorCode != "" {
					t.Log(mode, scenario.ID, step+1, errorCode, elapsed)
				} else {
					t.Log(mode, scenario.ID, step+1, *turn.AssistantMessage.Mode, elapsed)
				}
			}
		}
		// Ownership and empty retrieval are exercised against the real SQL path.
		logs.TakeAll()
		session, _ := chat.CreateSession(ctx, f.memberB, "No document access")
		_, err = chat.GetSession(ctx, f.memberA, session.ID, 20, 0)
		if err == nil {
			t.Fatal("cross-session access")
		}
		turn, err := chat.SendMessage(ctx, f.memberB, session.ID, uuid.New(), "Как получить телескоп Меридиан?")
		if len(turn.AssistantMessage.Sources) != 0 {
			t.Fatal("no-access citation leak")
		}
		controlCorrect := err == nil && turn.AssistantMessage.Mode != nil && *turn.AssistantMessage.Mode == domain.ChatModeNoSources
		entries := []map[string]any{}
		for _, entry := range logs.TakeAll() {
			fields := entry.ContextMap()
			fields["event"] = entry.Message
			entries = append(entries, fields)
		}
		controls = append(controls, map[string]any{"mode": mode, "no_document_access": turn, "logs": entries, "cross_session_denied": true,
			"expected_mode": domain.ChatModeNoSources, "control_correct": controlCorrect, "error": fmt.Sprint(err)})
		if !controlCorrect {
			t.Log("quality control failed: empty authorized corpus must produce no_sources; see controls.json")
		}
	}
	data, _ := json.MarshalIndent(controls, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "controls.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
