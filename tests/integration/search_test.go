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

func TestIntegrationSearchHTTPIdentityACLAndPublication(t *testing.T) {
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
	chunkRepo := postgres.NewChunkRepository(db)
	access := service.NewDocumentAccessService(postgres.NewDocumentAccessRepository(db), docs, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	search := service.NewSearchService(postgres.NewSearchRepository(db))
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{
		Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access,
		Search: search, MaxUploadBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	newClient := func() *documentClient {
		jar, _ := cookiejar.New(nil)
		return &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	}
	admin, memberA, memberB, anonymous := newClient(), newClient(), newClient(), newClient()
	anonymous.json("POST", "/api/v1/search", `{"query":"согласование"}`, 401)
	admin.login("admin")
	for _, username := range []string{"member-a", "member-b"} {
		admin.json("POST", "/api/v1/users", `{"username":"`+username+`","password":"integration-password-123"}`, 201)
	}
	memberA.login("member-a")
	memberB.login("member-b")
	userA, err := users.GetByUsername(ctx, "member-a")
	if err != nil {
		t.Fatal(err)
	}
	upload := func(filename, content string) service.DocumentDetails {
		t.Helper()
		var doc service.DocumentDetails
		if err := json.Unmarshal(admin.upload(filename, []byte(content), 201), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	docA := upload("policy-a.txt", "Original A")
	docB := upload("policy-b.txt", "Original B")
	pending := upload("pending.txt", "Pending original")
	seed := func(version domain.DocumentVersion, content string, page *int, section, title *string) {
		t.Helper()
		if err := chunkRepo.ReplaceVersionChunks(ctx, version.ID, []domain.DocumentChunk{{ChunkIndex: 3, Content: content, PageNumber: page, SectionNumber: section, SectionTitle: title}}); err != nil {
			t.Fatal(err)
		}
	}
	seed(docA.LatestVersion, "Архивный порядок согласования", nil, nil, nil)
	seed(docB.LatestVersion, "Согласование: чужойсекрет", nil, nil, nil)
	seed(pending.LatestVersion, "Согласование: ожидающийсекрет", nil, nil, nil)
	for _, doc := range []service.DocumentDetails{docA, docB} {
		if err := docs.UpdateVersionStatus(ctx, doc.ID, doc.LatestVersion.ID, domain.VersionReady); err != nil {
			t.Fatal(err)
		}
	}
	admin.json("POST", "/api/v1/documents/"+docA.ID.String()+"/access", `{"user_id":"`+userA.ID.String()+`"}`, 201)
	find := func(client *documentClient, query string, limit int) []domain.SearchResult {
		t.Helper()
		payload, _ := json.Marshal(domain.SearchQuery{Query: query, Limit: limit})
		data := client.json("POST", "/api/v1/search", string(payload), 200)
		var result struct {
			Query, Mode string
			Results     []domain.SearchResult
			Total       int
		}
		if err := json.Unmarshal(data, &result); err != nil || result.Mode != "search" || result.Query != strings.TrimSpace(query) || result.Total != len(result.Results) || result.Results == nil || len(result.Results) > domain.MaxSearchLimit {
			t.Fatal("invalid search response", err, string(data))
		}
		if strings.Contains(string(data), "storage_key") || strings.Contains(string(data), "lease_token") {
			t.Fatal("internal metadata in search response")
		}
		sources, err := json.Marshal(result.Results)
		if err != nil {
			t.Fatal(err)
		}
		if client == memberA && (strings.Contains(string(sources), docB.ID.String()) || strings.Contains(string(sources), docB.LatestVersion.ID.String()) || strings.Contains(string(sources), "чужойсекрет")) {
			t.Fatal("hidden source or metadata leaked")
		}
		return result.Results
	}
	if len(find(admin, "согласование", 30)) != 2 || len(find(memberA, "согласование", 30)) != 1 || len(find(memberB, "согласование", 30)) != 0 {
		t.Fatal("HTTP actor or ACL filtering failed")
	}
	if len(find(memberA, "чужойсекрет", 10)) != 0 || len(find(memberA, "несуществующееслово", 10)) != 0 {
		t.Fatal("hidden and missing source queries should both be empty")
	}
	memberA.request("POST", "/api/v1/search", []byte(`{"query":"согласование"}`), "application/json", "", 403)
	memberA.json("POST", "/api/v1/search", `{"query":"согласование","role":"admin"}`, 400)
	if len(find(admin, "согласование", 1)) != 1 {
		t.Fatal("total must count only the limited result slice")
	}
	// A real upload creates version 2; ready alone must not expose it before publication.
	body, media, err := versionMultipart("policy-a-v2.txt", []byte("Original A version 2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := admin.request("POST", "/api/v1/documents/"+docA.ID.String()+"/versions", body, media, admin.csrf, 201)
	var next domain.DocumentVersion
	if err := json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	page, section, title := 12, "2.3.1", "Согласование документов"
	seed(next, "Актуальный порядок согласования служебной записки", &page, &section, &title)
	if err := docs.UpdateVersionStatus(ctx, docA.ID, next.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if results := find(memberA, "согласование", 10); len(results) != 1 || results[0].DocumentVersionID != docA.LatestVersion.ID {
		t.Fatal("unpublished version exposed")
	}
	if err := docs.SetActiveVersion(ctx, docA.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	results := find(memberA, "согласования", 10)
	if len(results) != 1 || results[0].DocumentID != docA.ID || results[0].DocumentVersionID != next.ID || results[0].VersionNumber != 2 || results[0].PageNumber == nil || *results[0].PageNumber != page || results[0].SectionNumber == nil || *results[0].SectionNumber != section || results[0].SectionTitle == nil || *results[0].SectionTitle != title || results[0].ChunkID == uuid.Nil {
		t.Fatal("published citation incorrect")
	}
	if len(find(memberA, "архивный", 10)) != 0 {
		t.Fatal("historical version searchable")
	}
	for _, status := range []domain.VersionStatus{domain.VersionProcessing, domain.VersionFailed, domain.VersionUploaded} {
		if err := docs.UpdateVersionStatus(ctx, pending.ID, pending.LatestVersion.ID, status); err != nil {
			t.Fatal(err)
		}
		if len(find(admin, "ожидающийсекрет", 10)) != 0 {
			t.Fatal("non-ready source searchable")
		}
	}
	admin.json("DELETE", "/api/v1/documents/"+docA.ID.String()+"/access/"+userA.ID.String(), "", 204)
	if len(find(memberA, "согласование", 10)) != 0 {
		t.Fatal("HTTP revoke did not affect next search")
	}
	admin.json("POST", "/api/v1/documents/"+docA.ID.String()+"/access", `{"user_id":"`+userA.ID.String()+`"}`, 201)
	if len(find(memberA, "согласование", 10)) != 1 {
		t.Fatal("HTTP grant did not affect next search")
	}
	if err := users.SetActive(ctx, userA.ID, false); err != nil {
		t.Fatal(err)
	}
	memberA.json("POST", "/api/v1/search", `{"query":"согласование"}`, 401)
}
