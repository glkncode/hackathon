package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Golden output of the actual Python DOCX parser/chunker, including complete
// table rows and inherited metadata. The source hash prevents silent drift.
func workingGroupChunks(t *testing.T) []domain.DocumentChunk {
	t.Helper()
	paths, err := filepath.Glob("../../data/demo/*создании_рабоч*.docx")
	if err != nil || len(paths) != 1 {
		t.Fatal("working group DOCX missing", err)
	}
	source, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/working_group_ingestion.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceSHA256 string                 `json:"source_sha256"`
		Chunks       []domain.DocumentChunk `json:"chunks"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	if hex.EncodeToString(sum[:]) != fixture.SourceSHA256 {
		t.Fatal("DOCX changed; regenerate fixture with the real Python ingestion pipeline")
	}
	if len(fixture.Chunks) != 36 {
		t.Fatal("unexpected ingestion chunk count")
	}
	return fixture.Chunks
}
func TestIntegrationSearchNaturalQuestionsHTTP(t *testing.T) {
	db, ctx := testDatabase(t)
	log := zap.NewNop()
	users := postgres.NewUserRepository(db)
	if err := service.BootstrapAdmin(ctx, users, log, service.BootstrapConfig{Enabled: true, Username: "admin", Password: "integration-password-123"}); err != nil {
		t.Fatal(err)
	}
	admin, err := users.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	docs := postgres.NewDocumentRepository(db)
	doc, version, err := docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Распоряжение о создании рабочей группы", CreatedBy: admin.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "working-group.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", SizeBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewChunkRepository(db).ReplaceVersionChunks(ctx, version.ID, workingGroupChunks(t)); err != nil {
		t.Fatal(err)
	}
	if err := docs.UpdateVersionStatus(ctx, doc.ID, version.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	auth, err := service.NewAuthService(users, postgres.NewSessionRepository(db), log, service.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	access := service.NewDocumentAccessService(postgres.NewDocumentAccessRepository(db), docs, users, log)
	search := service.NewSearchService(postgres.NewSearchRepository(db))
	chat := service.NewChatService(postgres.NewChatRepository(db), search, log)
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, Search: search, Chat: chat, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	client.login("admin")
	for _, tc := range []struct {
		query   string
		section string
	}{
		{"Состав рабочей группы", "1"},
		{"Кто входит в состав рабочей группы?", "1"},
		{"За что отвечает секретарь рабочей группы?", "4"},
		{"Кто контролирует исполнение распоряжения?", "6"},
		{"организовать работу распределить задачи", "3"},
		{"Какие обязанности у руководителя рабочей группы?", "3"},
		{"Что должен делать руководитель рабочей группы?", "3"},
		{"Какие обязанности у секретаря рабочей группы?", "4"},
		{"Какая зарплата у руководителя рабочей группы?", ""},
		{"Какая ответственность руководителя рабочей группы за утечку данных?", ""},
		{"Как руководителю рабочей группы оформить отпуск?", ""},
		{"Какие обязанности у бухгалтера рабочей группы?", ""},
		{"Какие обязанности?", ""},
		{"Как настроить PostgreSQL репликацию?", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			subClient := *client
			subClient.t = t
			var parsed string
			if err := db.QueryRowContext(ctx, `SELECT websearch_to_tsquery('russian', $1)::text`, tc.query).Scan(&parsed); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(domain.SearchQuery{Query: tc.query})
			data := subClient.json("POST", "/api/v1/search", string(payload), 200)
			var response struct {
				Results []domain.SearchResult
				Total   int
			}
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			t.Logf("websearch_to_tsquery=%s; results=%d", parsed, response.Total)
			var session domain.ChatSession
			if err := json.Unmarshal(subClient.json("POST", "/api/v1/chat/sessions", `{}`, 201), &session); err != nil {
				t.Fatal(err)
			}
			message, _ := json.Marshal(map[string]string{"content": tc.query, "client_message_id": uuid.NewString()})
			turn := subClient.json("POST", "/api/v1/chat/sessions/"+session.ID.String()+"/messages", string(message), 200)
			var reply struct {
				Mode    string
				Sources []domain.ChatMessageSource
			}
			if err := json.Unmarshal(turn, &reply); err != nil {
				t.Fatal(err)
			}
			if tc.section == "" {
				if response.Total != 0 || reply.Mode != "no_sources" || len(reply.Sources) != 0 {
					t.Fatalf("unsupported question returned sources: %s", data)
				}
				return
			}
			if response.Total == 0 {
				t.Fatal("expected relevant source")
				return
			}
			first := response.Results[0]
			if reply.Mode != "search" || len(reply.Sources) == 0 || reply.Sources[0].ChunkID != first.ChunkID {
				t.Fatalf("chat did not use the same retrieval: %s", turn)
			}
			if first.SectionNumber == nil || *first.SectionNumber != tc.section || first.DocumentID != doc.ID || first.DocumentVersionID != version.ID || first.PageNumber != nil {
				t.Fatalf("incorrect citation: %+v", first)
			}
			if first.SectionTitle != nil {
				t.Fatalf("numbered instruction is not a section heading: %q", *first.SectionTitle)
			}
			assertCitation := func(source domain.ChatMessageSource) {
				t.Helper()
				if source.ChunkID != first.ChunkID || source.SectionNumber == nil || *source.SectionNumber != tc.section || source.SectionTitle != nil {
					t.Fatalf("chat citation differs from search: %+v", source)
				}
			}
			assertCitation(reply.Sources[0])
			replay := subClient.json("POST", "/api/v1/chat/sessions/"+session.ID.String()+"/messages", string(message), 200)
			if string(replay) != string(turn) {
				t.Fatal("idempotent replay changed citation metadata")
			}
			var history domain.ChatHistory
			if err := json.Unmarshal(subClient.json("GET", "/api/v1/chat/sessions/"+session.ID.String(), "", 200), &history); err != nil {
				t.Fatal(err)
			}
			if len(history.Messages) != 2 || len(history.Messages[1].Sources) == 0 {
				t.Fatal("missing history citation")
			}
			assertCitation(history.Messages[1].Sources[0])
		})
	}
}
