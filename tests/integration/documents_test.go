package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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

type documentClient struct {
	t      *testing.T
	ctx    context.Context
	base   string
	client *http.Client
	csrf   string
}

func (c *documentClient) request(method, path string, body []byte, contentType, csrf string, want int) ([]byte, http.Header) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Origin", "http://app.test")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		c.t.Fatal(err)
	}
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: got %d want %d, %s", method, path, res.StatusCode, want, data)
	}
	return data, res.Header
}
func (c *documentClient) json(method, path, payload string, want int) []byte {
	c.t.Helper()
	data, _ := c.request(method, path, []byte(payload), "application/json", c.csrf, want)
	return data
}
func (c *documentClient) login(username string) {
	c.t.Helper()
	data, _ := json.Marshal(map[string]string{"username": username, "password": "integration-password-123"})
	_, headers := c.request("POST", "/api/v1/auth/login", data, "application/json", "", 200)
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		if cookie.Name == "ai_csrf" {
			c.csrf = cookie.Value
		}
	}
	if c.csrf == "" {
		c.t.Fatal("missing CSRF cookie")
	}
}
func (c *documentClient) upload(filename string, contents []byte, want int) []byte {
	c.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := w.WriteField("title", "Инструкция"); err != nil {
		c.t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		c.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		c.t.Fatal(err)
	}
	data, _ := c.request("POST", "/api/v1/documents", b.Bytes(), w.FormDataContentType(), c.csrf, want)
	return data
}

func TestIntegrationUsersDocumentsACL(t *testing.T) {
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
	documents := postgres.NewDocumentRepository(db)
	access := service.NewDocumentAccessService(postgres.NewDocumentAccessRepository(db), documents, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	documentService := service.NewDocumentService(documents, access, storage, log)
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: documentService, Access: access, MaxUploadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	newClient := func() *documentClient {
		jar, _ := cookiejar.New(nil)
		return &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	}
	admin, member, other, anonymous := newClient(), newClient(), newClient(), newClient()
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/users"}, {"POST", "/api/v1/users"}, {"GET", "/api/v1/documents"}, {"POST", "/api/v1/documents"}, {"GET", "/api/v1/documents/" + uuid.NewString() + "/download"}} {
		anonymous.json(route.method, route.path, `{}`, 401)
	}
	admin.login("admin")
	create := func(name string) domain.User {
		t.Helper()
		data := admin.json("POST", "/api/v1/users", `{"username":"`+name+`","password":"integration-password-123"}`, 201)
		var u domain.User
		if err := json.Unmarshal(data, &u); err != nil {
			t.Fatal(err)
		}
		if u.ID == uuid.Nil || u.Role != domain.RoleMember || !u.IsActive || strings.Contains(string(data), "password") {
			t.Fatal("invalid member response")
		}
		return u
	}
	employee, outsider := create("Employee_1"), create("Employee_2")
	admin.json("POST", "/api/v1/users", `{"username":"EMPLOYEE_1","password":"integration-password-123"}`, 409)
	admin.json("POST", "/api/v1/users", `{"username":"new-admin","password":"integration-password-123","role":"admin"}`, 400)
	admin.json("POST", "/api/v1/users", `{"username":"bad name","password":"integration-password-123"}`, 400)
	admin.json("POST", "/api/v1/users", `{"username":"short-password","password":"short"}`, 400)
	for _, query := range []string{"?limit=0", "?limit=101", "?offset=-1", "?limit=2&limit=3", "?limit=%xx"} {
		admin.json("GET", "/api/v1/users"+query, "", 400)
	}
	listed := admin.json("GET", "/api/v1/users?limit=1&offset=1", "", 200)
	var page struct {
		Items []domain.User `json:"items"`
		Total int64         `json:"total"`
	}
	if err := json.Unmarshal(listed, &page); err != nil || len(page.Items) != 1 || page.Total != 3 || strings.Contains(string(listed), "password") {
		t.Fatal("users pagination failed")
	}
	member.login(employee.Username)
	other.login(outsider.Username)
	member.json("POST", "/api/v1/users", `{"username":"evil","password":"integration-password-123"}`, 403)
	member.json("GET", "/api/v1/users", "", 403)
	member.upload("file.txt", []byte("hello"), 403)
	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	payload := admin.upload(`..\..\private.pdf`, pdf, 201)
	var uploaded service.DocumentDetails
	if err := json.Unmarshal(payload, &uploaded); err != nil || uploaded.ID == uuid.Nil || uploaded.LatestVersion.VersionNumber != 1 || uploaded.LatestVersion.OriginalFilename != "private.pdf" || strings.Contains(string(payload), "storage_key") {
		t.Fatalf("invalid upload: %s %v", payload, err)
	}
	id := uploaded.ID.String()
	path := "/api/v1/documents/" + id
	admin.upload("second.txt", []byte("Another document"), 201)
	admin.upload("spoof.pdf", []byte("not a PDF"), 400)
	admin.upload("spoof.docx", []byte("not a ZIP"), 400)
	admin.upload("binary.txt", []byte{0, 1, 2}, 400)
	admin.upload("big.txt", bytes.Repeat([]byte("x"), 1025), 413)
	admin.upload("wrong.exe", []byte("text"), 400)
	assertDocuments := func(c *documentClient, want int) {
		t.Helper()
		data := c.json("GET", "/api/v1/documents?limit=20&offset=0", "", 200)
		var p struct {
			Items []domain.Document `json:"items"`
			Total int               `json:"total"`
		}
		if err := json.Unmarshal(data, &p); err != nil || p.Total != want || len(p.Items) != want || p.Items == nil {
			t.Fatalf("document page: %s %v", data, err)
		}
	}
	assertDocuments(admin, 2)
	assertDocuments(member, 0)
	denied := member.json("GET", path, "", 404)
	unknown := member.json("GET", "/api/v1/documents/"+uuid.NewString(), "", 404)
	if !bytes.Equal(denied, unknown) {
		t.Fatal("denied response reveals document existence")
	}
	member.json("GET", path+"/download", "", 404)
	for _, method := range []string{"GET", "POST"} {
		member.json(method, path+"/access", `{"user_id":"`+outsider.ID.String()+`"}`, 403)
	}
	member.json("DELETE", path+"/access/"+outsider.ID.String(), "", 403)
	grant := `{"user_id":"` + employee.ID.String() + `"}`
	admin.request("POST", path+"/access", []byte(grant), "application/json", "", 403)
	admin.request("POST", path+"/access", []byte(grant), "application/json", "wrong", 403)
	admin.json("POST", path+"/access", grant, 201)
	admin.json("POST", path+"/access", grant, 409)
	admin.json("POST", path+"/access", `{"user_id":"`+uuid.NewString()+`"}`, 404)
	adminUser, err := users.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin.json("POST", path+"/access", `{"user_id":"`+adminUser.ID.String()+`"}`, 400)
	if err := users.SetActive(ctx, outsider.ID, false); err != nil {
		t.Fatal(err)
	}
	admin.json("POST", path+"/access", `{"user_id":"`+outsider.ID.String()+`"}`, 400)
	other.json("GET", "/api/v1/documents", "", 401)
	if err := users.SetActive(ctx, outsider.ID, true); err != nil {
		t.Fatal(err)
	}
	assertDocuments(member, 1)
	assertDocuments(other, 0)
	member.json("GET", path, "", 200)
	downloaded, headers := member.request("GET", path+"/download", nil, "", "", 200)
	if !bytes.Equal(downloaded, pdf) || headers.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") {
		t.Fatal("unsafe or altered download")
	}
	other.json("GET", path+"/download", "", 404)
	acl := admin.json("GET", path+"/access", "", 200)
	if err := json.Unmarshal(acl, &page); err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != employee.ID || strings.Contains(string(acl), "password") {
		t.Fatal("invalid access list")
	}
	admin.json("DELETE", path+"/access/"+employee.ID.String(), "", 204)
	admin.json("DELETE", path+"/access/"+employee.ID.String(), "", 204)
	assertDocuments(member, 0)
	member.json("GET", path, "", 404)
	member.json("GET", path+"/download", "", 404)
	if _, err := documentService.GetVersion(ctx, employee, uploaded.ID, uploaded.LatestVersion.ID); err != domain.ErrNotFound {
		t.Fatalf("version bypassed revoked ACL: %v", err)
	}
	member.json("GET", "/api/v1/auth/me", "", 200) // Revoking ACL preserves sessions.
	admin.json("GET", path+"/download", "", 200)
	// Even an existing ACL cannot authorize an inactive account.
	admin.json("POST", path+"/access", grant, 201)
	if err := users.SetActive(ctx, employee.ID, false); err != nil {
		t.Fatal(err)
	}
	member.json("GET", path, "", 401)
	member.json("GET", path+"/download", "", 401)
	if _, err := auth.Login(ctx, employee.Username, "integration-password-123"); err != service.ErrInvalidCredentials {
		t.Fatal("inactive user logged in")
	}
	preflight, _ := http.NewRequestWithContext(ctx, "OPTIONS", server.URL+path+"/access/"+employee.ID.String(), nil)
	preflight.Header.Set("Origin", "http://app.test")
	preflight.Header.Set("Access-Control-Request-Method", "DELETE")
	preflight.Header.Set("Access-Control-Request-Headers", "X-CSRF-Token")
	res, err := admin.client.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || !strings.Contains(res.Header.Get("Access-Control-Allow-Methods"), "DELETE") {
		t.Fatal("DELETE preflight denied")
	}
}
