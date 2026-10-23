package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ragHTTPFixture struct {
	db            *sql.DB
	ctx           context.Context
	admin, member *documentClient
	owner         domain.User
	docs          *postgres.DocumentRepository
	acl           *postgres.DocumentAccessRepository
	chat          *service.ChatService
}

func newRAGHTTPFixture(t *testing.T, python http.Handler) ragHTTPFixture {
	t.Helper()
	db, ctx := testDatabase(t)
	log := zap.NewNop()
	users := postgres.NewUserRepository(db)
	if err := service.BootstrapAdmin(ctx, users, log, service.BootstrapConfig{Enabled: true, Username: "admin", Password: "integration-password-123"}); err != nil {
		t.Fatal(err)
	}
	auth, err := service.NewAuthService(users, postgres.NewSessionRepository(db), log, service.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	docs := postgres.NewDocumentRepository(db)
	acl := postgres.NewDocumentAccessRepository(db)
	access := service.NewDocumentAccessService(acl, docs, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	upstream := httptest.NewServer(python)
	t.Cleanup(upstream.Close)
	ai, err := aiclient.New(aiclient.Config{URL: upstream.URL, Token: "rag-integration-token", Timeout: 5 * time.Second, GenerateTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ai.Close)
	search := service.NewSearchService(postgres.NewSearchRepository(db))
	chat, err := service.NewChatServiceWithRAG(postgres.NewChatRepository(db), search, ai, log, service.RAGConfig{Timeout: 5 * time.Second, MaxSources: 8, MaxHistoryMessages: 10})
	if err != nil {
		t.Fatal(err)
	}
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, Search: search, Chat: chat, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := func() *documentClient {
		jar, _ := cookiejar.New(nil)
		return &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	}
	admin, member := client(), client()
	admin.login("admin")
	admin.json("POST", "/api/v1/users", `{"username":"rag-member","password":"integration-password-123"}`, 201)
	member.login("rag-member")
	owner, err := users.GetByUsername(ctx, "rag-member")
	if err != nil {
		t.Fatal(err)
	}
	return ragHTTPFixture{db: db, ctx: ctx, admin: admin, member: member, owner: owner, docs: docs, acl: acl, chat: chat}
}

func (f ragHTTPFixture) seed(t *testing.T, title, content string, grant bool) domain.Document {
	t.Helper()
	admin, err := postgres.NewUserRepository(f.db).GetByUsername(f.ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d, v, err := f.docs.CreateDocumentWithVersion(f.ctx, domain.Document{Title: title, CreatedBy: admin.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "rules.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewChunkRepository(f.db).ReplaceVersionChunks(f.ctx, v.ID, []domain.DocumentChunk{{Content: content}}); err != nil {
		t.Fatal(err)
	}
	if err := f.docs.UpdateVersionStatus(f.ctx, d.ID, v.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if grant {
		if _, err := f.acl.GrantAccess(f.ctx, domain.DocumentAccess{DocumentID: d.ID, UserID: f.owner.ID, GrantedBy: admin.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestIntegrationRAGHTTPACLAndTransitiveHistory(t *testing.T) {
	var calls atomic.Int32
	var clarify, unavailable atomic.Bool
	requests := make(chan aiclient.GenerateRequest, 10)
	f := newRAGHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/internal/v1/rag/generate" || r.Header.Get("X-Service-Token") != "rag-integration-token" || r.Header.Get("Cookie") != "" {
			t.Error("RAG authentication mismatch")
		}
		var input aiclient.GenerateRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			t.Error(err)
		}
		requests <- input
		w.Header().Set("Content-Type", "application/json")
		if unavailable.Load() {
			w.WriteHeader(404)
			return
		}
		answer := "пересказсекретаA"
		if input.Query == "бетадокумент" {
			answer = "пересказсекретаB"
		}
		for _, h := range input.History {
			if strings.Contains(h.Content, "пересказсекретаA") {
				answer = "переносвторичногоA"
			}
		}
		result := aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: []uuid.UUID{input.Sources[0].ChunkID}}
		if clarify.Load() {
			question := "Уточнение с секретом B"
			result = aiclient.GenerateResponse{Status: aiclient.GenerateNeedsClarification, ClarificationQuestion: &question, UsedSourceIDs: []uuid.UUID{}}
		}
		json.NewEncoder(w).Encode(result)
	}))
	a := f.seed(t, "Документ A", "альфадокумент секретA", true)
	b := f.seed(t, "Документ B", "бетадокумент секретB", true)
	f.seed(t, "Скрытый документ", "альфадокумент чужойсекрет", false)
	const root = "/api/v1/chat/sessions"
	var session domain.ChatSession
	if err := json.Unmarshal(f.member.json("POST", root, `{}`, 201), &session); err != nil {
		t.Fatal(err)
	}
	path := root + "/" + session.ID.String()
	keyA, keyB := uuid.NewString(), uuid.NewString()
	payload := func(query, key string) string {
		body, _ := json.Marshal(map[string]string{"content": query, "client_message_id": key})
		return string(body)
	}
	first := f.member.json("POST", path+"/messages", payload("альфадокумент", keyA), 200)
	input := <-requests
	if len(input.Sources) != 1 || strings.Contains(input.Sources[0].Content, "чужойсекрет") || !strings.Contains(string(first), `"mode":"generated"`) {
		t.Fatal("ACL before model failed", string(first))
	}
	second := f.member.json("POST", path+"/messages", payload("бетадокумент", keyB), 200)
	input = <-requests
	if len(input.History) != 2 || !strings.Contains(string(second), "переносвторичногоA") {
		t.Fatal("history not used", string(second))
	}
	if err := f.acl.RevokeAccess(f.ctx, a.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	history := f.member.json("GET", path, "", 200)
	for _, data := range [][]byte{history, f.member.json("POST", path+"/messages", payload("альфадокумент", keyA), 200), f.member.json("POST", path+"/messages", payload("бетадокумент", keyB), 200)} {
		if strings.Contains(string(data), "пересказсекретаA") || strings.Contains(string(data), "переносвторичногоA") || strings.Contains(string(data), a.ID.String()) || !strings.Contains(string(data), domain.ChatContentUnavailable) {
			t.Fatal("generated text leaked after revoke", string(data))
		}
	}
	if calls.Load() != 2 {
		t.Fatal("replay called model")
	}
	third := f.member.json("POST", path+"/messages", payload("бетадокумент", uuid.NewString()), 200)
	input = <-requests
	raw, _ := json.Marshal(input)
	if strings.Contains(string(raw), "пересказсекретаA") || strings.Contains(string(raw), "переносвторичногоA") || strings.Contains(string(raw), "секретA") || !strings.Contains(string(third), "пересказсекретаB") {
		t.Fatal("closed content entered later model context")
	}
	clarify.Store(true)
	clarifyKey := uuid.NewString()
	clarification := f.member.json("POST", path+"/messages", payload("бетадокумент", clarifyKey), 200)
	<-requests
	if !strings.Contains(string(clarification), `"mode":"clarification"`) || !strings.Contains(string(clarification), "Уточнение с секретом B") {
		t.Fatal(string(clarification))
	}
	if err := f.acl.RevokeAccess(f.ctx, b.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	redacted := f.member.json("POST", path+"/messages", payload("бетадокумент", clarifyKey), 200)
	if strings.Contains(string(redacted), "Уточнение с секретом B") || !strings.Contains(string(redacted), domain.ChatContentUnavailable) {
		t.Fatal("clarification bypassed redaction")
	}
	noSources := f.member.json("POST", path+"/messages", payload("бетадокумент", uuid.NewString()), 200)
	if !strings.Contains(string(noSources), `"mode":"no_sources"`) || calls.Load() != 4 {
		t.Fatal("empty retrieval invoked LLM")
	}
	admin, err := postgres.NewUserRepository(f.db).GetByUsername(f.ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.acl.GrantAccess(f.ctx, domain.DocumentAccess{DocumentID: b.ID, UserID: f.owner.ID, GrantedBy: admin.ID}); err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	fallback := f.member.json("POST", path+"/messages", payload("бетадокумент", uuid.NewString()), 200)
	<-requests
	if !strings.Contains(string(fallback), `"mode":"search"`) || strings.Contains(string(fallback), "Уточнение с секретом B") {
		t.Fatal("404 did not fall back", string(fallback))
	}
	// An admin cannot use another user's session to retrieve generated history.
	f.admin.json("GET", path, "", 404)
}

func TestIntegrationRAGConcurrentSendAndArchive(t *testing.T) {
	for _, action := range []string{"duplicate", "archive", "revoke"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once, releaseOnce sync.Once
			openGate := func() { releaseOnce.Do(func() { close(release) }) }
			defer openGate()
			f := newRAGHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input aiclient.GenerateRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				answer := "Согласуйте документ"
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: []uuid.UUID{input.Sources[0].ChunkID}})
			}))
			doc := f.seed(t, "Rules", "согласование", true)
			session, err := f.chat.CreateSession(f.ctx, f.owner, "")
			if err != nil {
				t.Fatal(err)
			}
			const n = 6
			key := uuid.New()
			results := make(chan domain.ChatTurn, n)
			failures := make(chan error, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					turn, err := f.chat.SendMessage(f.ctx, f.owner, session.ID, key, "согласование")
					if err != nil {
						failures <- err
					} else {
						results <- turn
					}
				}()
			}
			select {
			case <-entered:
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
			if action == "archive" {
				// Generation is blocked in HTTP, while archive can still lock the
				// session. No transaction spans the Python request.
				if err := f.chat.ArchiveSession(f.ctx, f.owner, session.ID); err != nil {
					t.Fatal(err)
				}
			}
			if action == "revoke" {
				if err := f.acl.RevokeAccess(f.ctx, doc.ID, f.owner.ID); err != nil {
					t.Fatal(err)
				}
			}
			openGate()
			wg.Wait()
			close(results)
			close(failures)
			if action == "duplicate" {
				for err := range failures {
					t.Fatal(err)
				}
				var id uuid.UUID
				for turn := range results {
					if id == uuid.Nil {
						id = turn.AssistantMessage.ID
					}
					if id != turn.AssistantMessage.ID {
						t.Fatal("duplicate generated pair")
					}
				}
				messages, err := postgres.NewChatRepository(f.db).ListMessages(f.ctx, f.owner.ID, session.ID, 20, 0)
				if err != nil || len(messages) != 2 {
					t.Fatal("concurrent history damaged", err)
				}
			} else {
				for turn := range results {
					// A sender whose retrieval starts after revocation can safely
					// win this same key with a fixed no_sources response.
					if action != "revoke" || turn.AssistantMessage.Mode == nil || *turn.AssistantMessage.Mode != domain.ChatModeNoSources || len(turn.AssistantMessage.Sources) != 0 {
						t.Error("stale model answer saved after", action)
					}
				}
				for err := range failures {
					want := domain.ErrChatArchived
					if action == "revoke" {
						want = domain.ErrInvalidState
					}
					if !errors.Is(err, want) {
						t.Error(err)
					}
				}
				messages, err := postgres.NewChatRepository(f.db).ListMessages(f.ctx, f.owner.ID, session.ID, 20, 0)
				if err != nil {
					t.Fatal(err)
				}
				if action == "archive" && len(messages) != 0 {
					t.Fatal("archive race corrupted history")
				}
				if action == "revoke" {
					if len(messages) != 0 && len(messages) != 2 {
						t.Fatal("partial revoked turn")
					}
					for _, m := range messages {
						if m.Role == domain.ChatRoleAssistant && (m.Mode == nil || *m.Mode != domain.ChatModeNoSources || len(m.Sources) != 0) {
							t.Fatal("revoked generated content persisted")
						}
					}
				}
			}
		})
	}
}
