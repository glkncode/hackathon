package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func versionMultipart(filename string, contents []byte, fields map[string]string) ([]byte, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(contents); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return b.Bytes(), w.FormDataContentType(), nil
}

func TestIntegrationDocumentVersionHTTPAndRestart(t *testing.T) {
	db, ctx := testDatabase(t)
	log := zap.NewNop()
	users := postgres.NewUserRepository(db)
	if err := service.BootstrapAdmin(ctx, users, log, service.BootstrapConfig{Enabled: true, Username: "admin", Password: "integration-password-123"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	storage, err := service.NewLocalDocumentStorage(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	// Upgrade a real migration-3 document with an existing disk original.
	for {
		version, err := migrator.Version(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if version <= 3 {
			break
		}
		if err := migrator.Down(ctx, db, log); err != nil {
			t.Fatal(err)
		}
	}
	legacyFile, err := storage.Save(ctx, strings.NewReader("Legacy original"), "legacy.txt")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := users.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	legacyID, legacyVersionID := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO documents (id,title,created_by) VALUES ($1,'Legacy document',$2)`, legacyID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO document_versions (id,document_id,version_number,storage_key,original_filename,mime_type,size_bytes) VALUES ($1,$2,1,$3,$4,$5,$6)`, legacyVersionID, legacyID, legacyFile.Key, legacyFile.Filename, legacyFile.MIMEType, legacyFile.Size); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	start := func() (*httptest.Server, *service.DocumentService) {
		t.Helper()
		auth, err := service.NewAuthService(users, postgres.NewSessionRepository(db), log, service.AuthConfig{})
		if err != nil {
			t.Fatal(err)
		}
		repo := postgres.NewDocumentRepository(db)
		acl := service.NewDocumentAccessService(postgres.NewDocumentAccessRepository(db), repo, users, log)
		documents := service.NewDocumentService(repo, acl, storage, log)
		router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{AllowedOrigins: []string{"http://app.test"}}, httptransport.Modules{Users: service.NewUserService(users), Documents: documents, Access: acl, MaxUploadBytes: 1024})
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewServer(router)
		t.Cleanup(s.Close)
		return s, documents
	}
	server, documents := start()
	client := func() *documentClient {
		jar, _ := cookiejar.New(nil)
		return &documentClient{t: t, ctx: ctx, base: server.URL, client: &http.Client{Jar: jar}}
	}
	admin, member, other, anonymous := client(), client(), client(), client()
	admin.login("admin")
	legacyPath := "/api/v1/documents/" + legacyID.String()
	legacyData, _ := admin.request("GET", legacyPath+"/download", nil, "", "", 200)
	if string(legacyData) != "Legacy original" {
		t.Fatal("upgrade broke legacy download")
	}
	var legacyDetails service.DocumentDetails
	if err := json.Unmarshal(admin.json("GET", legacyPath, "", 200), &legacyDetails); err != nil || legacyDetails.ActiveVersion.ID != legacyVersionID || legacyDetails.ActiveVersion.SHA256 != "" || legacyDetails.ActiveVersion.Status != domain.VersionUploaded {
		t.Fatal("legacy metadata not compatible")
	}
	createUser := func(name string) domain.User {
		t.Helper()
		data := admin.json("POST", "/api/v1/users", `{"username":"`+name+`","password":"integration-password-123"}`, 201)
		var u domain.User
		if err := json.Unmarshal(data, &u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	user := createUser("employee")
	createUser("outsider")
	member.login("employee")
	other.login("outsider")
	firstData := []byte("The first original\n")
	response := admin.upload("v1.txt", firstData, 201)
	var details service.DocumentDetails
	if err := json.Unmarshal(response, &details); err != nil {
		t.Fatal(err)
	}
	id := details.ID
	v1 := details.ActiveVersion
	if v1.ID == uuid.Nil || v1.Status != domain.VersionUploaded || details.ActiveVersionID.UUID != v1.ID {
		t.Fatal("first version must be active and uploaded")
	}
	path := "/api/v1/documents/" + id.String()
	historyPath := path + "/versions"
	for _, c := range []*documentClient{anonymous, other, member} {
		status := 404
		if c == anonymous {
			status = 401
		}
		c.json("GET", historyPath, "", status)
		c.json("GET", historyPath+"/"+v1.ID.String(), "", status)
		c.json("GET", historyPath+"/"+v1.ID.String()+"/download", "", status)
	}
	admin.json("POST", path+"/access", `{"user_id":"`+user.ID.String()+`"}`, 201)
	upload := func(c *documentClient, name, contents string, fields map[string]string, csrf string, want int) domain.DocumentVersion {
		t.Helper()
		body, ct, err := versionMultipart(name, []byte(contents), fields)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := c.request("POST", historyPath, body, ct, csrf, want)
		var v domain.DocumentVersion
		if want == 201 {
			if err := json.Unmarshal(data, &v); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "storage_key") || strings.Contains(string(data), "StorageKey") || v.Status != domain.VersionUploaded {
				t.Fatalf("unsafe version response: %s", data)
			}
			digest := sha256.Sum256([]byte(contents))
			if v.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("wrong SHA-256")
			}
		}
		return v
	}
	upload(anonymous, "v2.txt", "two", nil, "", 401)
	upload(member, "v2.txt", "two", nil, member.csrf, 403)
	upload(admin, "v2.txt", "two", nil, "", 403)
	upload(admin, "v2.txt", "two", nil, "wrong", 403)
	evilBody, evilType, err := versionMultipart("evil.txt", []byte("rejected"), nil)
	if err != nil {
		t.Fatal(err)
	}
	evilRequest, err := http.NewRequestWithContext(ctx, "POST", admin.base+historyPath, bytes.NewReader(evilBody))
	if err != nil {
		t.Fatal(err)
	}
	evilRequest.Header.Set("Origin", "http://untrusted.test")
	evilRequest.Header.Set("X-CSRF-Token", admin.csrf)
	evilRequest.Header.Set("Content-Type", evilType)
	evilResponse, err := admin.client.Do(evilRequest)
	if err != nil {
		t.Fatal(err)
	}
	evilResponse.Body.Close()
	if evilResponse.StatusCode != 403 {
		t.Fatal("version upload accepted an untrusted Origin")
	}
	for _, field := range []string{"version_number", "status", "active_version_id"} {
		upload(admin, "v2.txt", "two", map[string]string{field: "99"}, admin.csrf, 400)
	}
	upload(admin, "bad.pdf", "not a PDF", nil, admin.csrf, 400)
	upload(admin, "large.txt", strings.Repeat("x", 1025), nil, admin.csrf, 413)
	v2 := upload(admin, `..\..\v2.txt`, "The second original", nil, admin.csrf, 201)
	v3 := upload(admin, "v3.txt", "The third original", nil, admin.csrf, 201)
	if v2.VersionNumber != 2 || v3.VersionNumber != 3 || v2.OriginalFilename != "v2.txt" {
		t.Fatal("wrong numbering or unsafe filename")
	}
	all := map[uuid.UUID]string{v1.ID: string(firstData), v2.ID: "The second original", v3.ID: "The third original"}
	// Two actual concurrent HTTP uploads, including independent disk originals.
	type result struct {
		version  domain.DocumentVersion
		contents string
		err      error
	}
	results := make(chan result, 2)
	for _, text := range []string{"Concurrent A", "Concurrent B"} {
		body, ct, err := versionMultipart("concurrent.txt", []byte(text), nil)
		if err != nil {
			t.Fatal(err)
		}
		go func(contents string, body []byte, ct string) {
			res := result{contents: contents}
			req, err := http.NewRequestWithContext(ctx, "POST", admin.base+historyPath, bytes.NewReader(body))
			if err != nil {
				res.err = err
				results <- res
				return
			}
			req.Header.Set("Origin", "http://app.test")
			req.Header.Set("X-CSRF-Token", admin.csrf)
			req.Header.Set("Content-Type", ct)
			reply, err := admin.client.Do(req)
			if err != nil {
				res.err = err
				results <- res
				return
			}
			payload, err := io.ReadAll(reply.Body)
			reply.Body.Close()
			if err == nil && reply.StatusCode != 201 {
				err = fmt.Errorf("concurrent upload status %d", reply.StatusCode)
			}
			if err == nil {
				err = json.Unmarshal(payload, &res.version)
			}
			res.err = err
			results <- res
		}(text, body, ct)
	}
	numbers := map[int]bool{}
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		numbers[r.version.VersionNumber] = true
		all[r.version.ID] = r.contents
	}
	if len(numbers) != 2 || !numbers[4] || !numbers[5] {
		t.Fatalf("concurrent versions: %v", numbers)
	}
	data := member.json("GET", historyPath+"?limit=2&offset=1", "", 200)
	var page struct {
		Items []domain.DocumentVersion `json:"items"`
		Total int64                    `json:"total"`
	}
	if err := json.Unmarshal(data, &page); err != nil || page.Total != 5 || len(page.Items) != 2 || page.Items[0].VersionNumber != 4 || page.Items[1].VersionNumber != 3 {
		t.Fatalf("history: %s %v", data, err)
	}
	other.json("GET", historyPath, "", 404)
	for versionID, contents := range all {
		endpoint := historyPath + "/" + versionID.String()
		member.json("GET", endpoint, "", 200)
		data, _ := member.request("GET", endpoint+"/download", nil, "", "", 200)
		if string(data) != contents {
			t.Fatal("old/new original changed")
		}
	}
	// An unpublished newest version is distinct from the active file.
	response = admin.json("GET", path, "", 200)
	if err := json.Unmarshal(response, &details); err != nil || details.LatestVersion.VersionNumber != 5 || details.ActiveVersion.ID != v1.ID {
		t.Fatal("latest and active conflated")
	}
	data, _ = member.request("GET", path+"/download", nil, "", "", 200)
	if !bytes.Equal(data, firstData) {
		t.Fatal("ordinary download changed on upload")
	}
	if err := documents.PublishReadyVersion(ctx, id, v2.ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("uploaded version published")
	}
	if err := documents.UpdateVersionStatus(ctx, id, v2.ID, domain.VersionProcessing); err != nil {
		t.Fatal(err)
	}
	if err := documents.UpdateVersionStatus(ctx, id, v2.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	data, _ = member.request("GET", path+"/download", nil, "", "", 200)
	if !bytes.Equal(data, firstData) {
		t.Fatal("status change activated a version")
	}
	if err := documents.PublishReadyVersion(ctx, id, v2.ID); err != nil {
		t.Fatal(err)
	}
	data, _ = member.request("GET", path+"/download", nil, "", "", 200)
	if string(data) != all[v2.ID] {
		t.Fatal("publication did not change ordinary download")
	}
	response = admin.upload("foreign.txt", []byte("Other document"), 201)
	var foreign service.DocumentDetails
	if err := json.Unmarshal(response, &foreign); err != nil {
		t.Fatal(err)
	}
	admin.json("POST", "/api/v1/documents/"+foreign.ID.String()+"/access", `{"user_id":"`+user.ID.String()+`"}`, 201)
	for _, c := range []*documentClient{admin, member} {
		c.json("GET", historyPath+"/"+foreign.ActiveVersion.ID.String(), "", 404)
		c.json("GET", historyPath+"/"+foreign.ActiveVersion.ID.String()+"/download", "", 404)
	}
	if err := documents.UpdateVersionStatus(ctx, foreign.ID, foreign.ActiveVersion.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if err := documents.PublishReadyVersion(ctx, id, foreign.ActiveVersion.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("published a foreign version")
	}
	// Reconstruct storage, services and HTTP while retaining PostgreSQL and files.
	server.Close()
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = service.NewLocalDocumentStorage(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	server, documents = start()
	for _, c := range []*documentClient{admin, member, other, anonymous} {
		c.base = server.URL
	}
	for versionID, contents := range all {
		data, _ = member.request("GET", historyPath+"/"+versionID.String()+"/download", nil, "", "", 200)
		if string(data) != contents {
			t.Fatal("restart lost a historical original")
		}
	}
	data, _ = member.request("GET", path+"/download", nil, "", "", 200)
	if string(data) != all[v2.ID] {
		t.Fatal("restart changed active version")
	}
	// ACL revocation applies to the entire history, without revoking the session.
	admin.json("DELETE", path+"/access/"+user.ID.String(), "", 204)
	member.json("GET", historyPath, "", 404)
	member.json("GET", historyPath+"/"+v1.ID.String()+"/download", "", 404)
	member.json("GET", "/api/v1/auth/me", "", 200)
}
