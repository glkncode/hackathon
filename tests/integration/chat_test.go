package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestIntegrationChatHTTP(t *testing.T) {
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
	defer storage.Close()
	chatRepo := postgres.NewChatRepository(db)
	search := service.NewSearchService(postgres.NewSearchRepository(db))
	chat := service.NewChatService(chatRepo, search, log)
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, Search: search, Chat: chat, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	client := func() *documentClient {
		jar, _ := cookiejar.New(nil)
		return &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	}
	admin, a, b, anonymous := client(), client(), client(), client()
	const root = "/api/v1/chat/sessions"
	anonymous.json("POST", root, `{}`, 401)
	admin.login("admin")
	for _, name := range []string{"chat-a", "chat-b"} {
		admin.json("POST", "/api/v1/users", `{"username":"`+name+`","password":"integration-password-123"}`, 201)
	}
	a.login("chat-a")
	b.login("chat-b")
	owner, err := users.GetByUsername(ctx, "chat-a")
	if err != nil {
		t.Fatal(err)
	}
	var doc service.DocumentDetails
	if err := json.Unmarshal(admin.upload("rules.txt", []byte("Правила согласования"), 201), &doc); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewChunkRepository(db).ReplaceVersionChunks(ctx, doc.LatestVersion.ID, []domain.DocumentChunk{{Content: "Согласование служебных записок: цитируемыйсекрет"}}); err != nil {
		t.Fatal(err)
	}
	if err := docs.UpdateVersionStatus(ctx, doc.ID, doc.LatestVersion.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	admin.json("POST", "/api/v1/documents/"+doc.ID.String()+"/access", `{"user_id":"`+owner.ID.String()+`"}`, 201)
	a.request("POST", root, []byte(`{}`), "application/json", "wrong-csrf", 403)
	var session domain.ChatSession
	if err := json.Unmarshal(a.json("POST", root, `{}`, 201), &session); err != nil || session.ID == uuid.Nil {
		t.Fatal(session, err)
	}
	path := root + "/" + session.ID.String()
	key := uuid.NewString()
	payload := `{"content":"согласование","client_message_id":"` + key + `"}`
	var response struct {
		UserMessageID      uuid.UUID                  `json:"user_message_id"`
		AssistantMessageID uuid.UUID                  `json:"assistant_message_id"`
		Mode               string                     `json:"mode"`
		Message            string                     `json:"message"`
		Sources            []domain.ChatMessageSource `json:"sources"`
	}
	data := a.json("POST", path+"/messages", payload, 200)
	if err := json.Unmarshal(data, &response); err != nil || response.Mode != "search" || len(response.Sources) != 1 || response.Sources[0].DocumentVersionID != doc.LatestVersion.ID || response.UserMessageID == uuid.Nil || response.AssistantMessageID == uuid.Nil || strings.Contains(response.Message, "цитируемыйсекрет") {
		t.Fatal(string(data), err)
	}
	if replay := a.json("POST", path+"/messages", payload, 200); string(replay) != string(data) {
		t.Fatal("replay changed persisted response")
	}
	a.json("POST", path+"/messages", strings.Replace(payload, "согласование", "другой вопрос", 1), 409)
	for _, other := range []*documentClient{admin, b} {
		other.json("GET", path, "", 404)
		other.json("POST", path+"/messages", payload, 404)
		other.json("DELETE", path, "", 404)
		list := other.json("GET", root, "", 200)
		if strings.Contains(string(list), session.ID.String()) {
			t.Fatal("foreign session listed")
		}
	}
	history := a.json("GET", path, "", 200)
	if !strings.Contains(string(history), "цитируемыйсекрет") || strings.Contains(string(history), "storage_key") {
		t.Fatal(string(history))
	}
	page := a.json("GET", path+"?limit=1&offset=1", "", 200)
	var paged domain.ChatHistory
	if err := json.Unmarshal(page, &paged); err != nil || len(paged.Messages) != 1 || paged.Messages[0].Role != domain.ChatRoleAssistant {
		t.Fatal(string(page), err)
	}
	admin.json("DELETE", "/api/v1/documents/"+doc.ID.String()+"/access/"+owner.ID.String(), "", 204)
	for _, result := range [][]byte{a.json("GET", path, "", 200), a.json("POST", path+"/messages", payload, 200)} {
		if strings.Contains(string(result), "цитируемыйсекрет") || strings.Contains(string(result), doc.ID.String()) || strings.Contains(string(result), doc.LatestVersion.ID.String()) || strings.Contains(string(result), response.Sources[0].ChunkID.String()) {
			t.Fatal("revoked metadata/content in history or replay", string(result))
		}
	}
	noSources := a.json("POST", path+"/messages", strings.Replace(payload, key, uuid.NewString(), 1), 200)
	if !strings.Contains(string(noSources), `"mode":"no_sources"`) || !strings.Contains(string(noSources), `"sources":[]`) {
		t.Fatal(string(noSources))
	}
	a.json("POST", path+"/messages", `{"content":5}`, 400)
	a.json("DELETE", path, "", 204)
	a.json("POST", path+"/messages", payload, 409)
	a.json("GET", path, "", 200)
	if list := a.json("GET", root, "", 200); strings.Contains(string(list), session.ID.String()) {
		t.Fatal("archived session listed")
	}
	if err := users.SetActive(ctx, owner.ID, false); err != nil {
		t.Fatal(err)
	}
	a.json("GET", path, "", 401)
}
