package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/repository/postgres"
	"github.com/google/uuid"
)

var workingGroupQuestions = []struct {
	query  string
	chunks []int
}{
	{"Состав рабочей группы", []int{10, 11, 12, 13, 14}},
	{"Кто входит в состав рабочей группы?", []int{10, 11, 12, 13, 14}},
	{"Какие обязанности у руководителя рабочей группы?", []int{16}},
	{"За что отвечает секретарь рабочей группы?", []int{17}},
	{"Кто контролирует исполнение распоряжения?", []int{19}},
}

func TestIntegrationWorkingGroupRAGBoundary(t *testing.T) { workingGroupPipeline(t, false) }

// Optional acceptance test against the unchanged Python service and real model.
// Unlike the protocol test, this checks generation rather than an HTTP double.
func TestIntegrationWorkingGroupQwen(t *testing.T) {
	if os.Getenv("TEST_RAG_URL") == "" || os.Getenv("TEST_RAG_TOKEN") == "" {
		t.Skip("set TEST_RAG_URL and TEST_RAG_TOKEN for real Python/Qwen acceptance")
	}
	workingGroupPipeline(t, true)
}

func workingGroupPipeline(t *testing.T, realModel bool) {
	t.Helper()
	requests := make(chan aiclient.GenerateRequest, 32)
	modelResults := make(chan aiclient.GenerateResponse, 32)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	python := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, aiclient.MaxGenerateRequestBytes+1))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		var input aiclient.GenerateRequest
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		requests <- input
		if realModel {
			base, err := url.Parse(os.Getenv("TEST_RAG_URL"))
			if err != nil {
				t.Error("invalid test RAG URL")
				w.WriteHeader(500)
				return
			}
			endpoint := base.ResolveReference(&url.URL{Path: "/internal/v1/rag/generate"})
			req, err := http.NewRequestWithContext(r.Context(), "POST", endpoint.String(), bytes.NewReader(raw))
			if err != nil {
				t.Error("invalid test RAG request")
				w.WriteHeader(500)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Service-Token", os.Getenv("TEST_RAG_TOKEN"))
			response, err := client.Do(req)
			if err != nil {
				t.Error("test Python service unavailable")
				w.WriteHeader(503)
				return
			}
			defer response.Body.Close()
			responseBody, err := io.ReadAll(io.LimitReader(response.Body, aiclient.MaxGenerateResponseBytes+1))
			if err != nil {
				t.Error("cannot read test Python response")
				w.WriteHeader(503)
				return
			}
			var result aiclient.GenerateResponse
			if response.StatusCode == 200 && json.Unmarshal(responseBody, &result) == nil {
				modelResults <- result
			}
			w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
			w.WriteHeader(response.StatusCode)
			w.Write(responseBody)
			return
		}
		// Query-independent extractive HTTP double, never production answers.
		var texts []string
		ids := make([]uuid.UUID, 0, len(input.Sources))
		for _, source := range input.Sources {
			texts = append(texts, source.Content)
			ids = append(ids, source.ChunkID)
		}
		answer := strings.Join(texts, "\n")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(aiclient.GenerateResponse{Status: aiclient.GenerateAnswered, Answer: &answer, UsedSourceIDs: ids})
	})
	f := newRAGHTTPFixtureWithTimeout(t, python, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f.ctx, f.member.ctx, f.admin.ctx = ctx, ctx, ctx
	admin, err := postgres.NewUserRepository(f.db).GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	doc, version, err := f.docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Модуль распоряжения о создании рабочей группы", CreatedBy: admin.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "working-group.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", SizeBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	chunks := workingGroupChunks(t)
	if realModel {
		// Exercise the real Go multipart client -> Python DOCX parser/chunker,
		// then persist that response, not a hand-built approximation of a table.
		ingestion, err := aiclient.New(aiclient.Config{URL: os.Getenv("TEST_RAG_URL"), Token: os.Getenv("TEST_RAG_TOKEN"), Timeout: 60 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer ingestion.Close()
		paths, err := filepath.Glob("../../data/demo/*создании_рабоч*.docx")
		if err != nil || len(paths) != 1 {
			t.Fatal("DOCX missing", err)
		}
		file, err := os.Open(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ingestion.Ingest(ctx, aiclient.Request{DocumentID: doc.ID, DocumentVersionID: version.ID, MIMEType: version.MIMEType, Filename: filepath.Base(paths[0]), File: file})
		if err != nil {
			t.Fatal("actual Python ingestion failed", err)
		}
		// The production client attaches the trusted request version ID; the
		// parser's golden fixture deliberately contains no database identities.
		for i := range chunks {
			chunks[i].DocumentVersionID = version.ID
		}
		if !reflect.DeepEqual(parsed.Chunks, chunks) {
			t.Fatal("real ingestion differs from the versioned fixture; inspect row/block integrity before updating expectations")
		}
		chunks = parsed.Chunks
		t.Log("real Python ingestion: 36 chunks; all text/metadata match fixture, including complete table rows")
	}
	if err := postgres.NewChunkRepository(f.db).ReplaceVersionChunks(ctx, version.ID, chunks); err != nil {
		t.Fatal(err)
	}
	// Subsequent fixture copies belong to other versions/documents. The
	// repository attaches each destination ID; never reuse the ingestion ID.
	for i := range chunks {
		chunks[i].DocumentVersionID = uuid.Nil
	}
	if err := f.docs.UpdateVersionStatus(ctx, doc.ID, version.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	if _, err := f.acl.GrantAccess(ctx, domain.DocumentAccess{DocumentID: doc.ID, UserID: f.owner.ID, GrantedBy: admin.ID}); err != nil {
		t.Fatal(err)
	}
	unpublished, err := f.docs.CreateVersion(ctx, doc.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "unpublished.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", SizeBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewChunkRepository(f.db).ReplaceVersionChunks(ctx, unpublished.ID, chunks); err != nil {
		t.Fatal(err)
	}
	if err := f.docs.UpdateVersionStatus(ctx, doc.ID, unpublished.ID, domain.VersionReady); err != nil {
		t.Fatal(err)
	}
	stored := map[uuid.UUID]domain.DocumentChunk{}
	rows, err := f.db.QueryContext(ctx, `SELECT id,chunk_index,content FROM document_chunks WHERE document_version_id=$1 ORDER BY chunk_index`, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c domain.DocumentChunk
		if err := rows.Scan(&c.ID, &c.ChunkIndex, &c.Content); err != nil {
			t.Fatal(err)
		}
		stored[c.ID] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(stored) != 36 {
		t.Fatal("incomplete ingestion fixture", err)
	}
	// Seed matching hidden and inactive versions. Their IDs/text must never
	// reach either Search HTTP or the Python request, including table expansion.
	for _, visible := range []bool{false, true} {
		d, v, err := f.docs.CreateDocumentWithVersion(ctx, domain.Document{Title: "Другой документ", CreatedBy: admin.ID}, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "other.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", SizeBytes: 1024})
		if err != nil {
			t.Fatal(err)
		}
		if err := postgres.NewChunkRepository(f.db).ReplaceVersionChunks(ctx, v.ID, chunks); err != nil {
			t.Fatal(err)
		}
		if visible {
			if _, err := f.acl.GrantAccess(ctx, domain.DocumentAccess{DocumentID: d.ID, UserID: f.owner.ID, GrantedBy: admin.ID}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := f.docs.UpdateVersionStatus(ctx, d.ID, v.ID, domain.VersionReady); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range workingGroupQuestions {
		t.Run(tc.query, func(t *testing.T) {
			member := *f.member
			member.t = t
			body, _ := json.Marshal(domain.SearchQuery{Query: tc.query, Limit: 10})
			var search struct{ Results []domain.SearchResult }
			if err := json.Unmarshal(member.json("POST", "/api/v1/search", string(body), 200), &search); err != nil {
				t.Fatal(err)
			}
			indexes := []int{}
			allowed := map[uuid.UUID]bool{}
			for _, source := range search.Results {
				chunk, ok := stored[source.ChunkID]
				if !ok || source.Content != chunk.Content || source.DocumentID != doc.ID || source.DocumentVersionID != version.ID {
					t.Fatal("source is fabricated, hidden, or wrong version", source)
				}
				indexes = append(indexes, chunk.ChunkIndex)
				allowed[source.ChunkID] = true
			}
			if !slices.Equal(indexes, tc.chunks) {
				t.Fatalf("wrong source chunks/top-k: got %v want %v", indexes, tc.chunks)
			}
			var session domain.ChatSession
			if err := json.Unmarshal(member.json("POST", "/api/v1/chat/sessions", `{}`, 201), &session); err != nil {
				t.Fatal(err)
			}
			message, _ := json.Marshal(map[string]string{"content": tc.query, "client_message_id": uuid.NewString()})
			var reply struct {
				Mode    string
				Message string
				Sources []domain.ChatMessageSource
			}
			if err := json.Unmarshal(member.json("POST", "/api/v1/chat/sessions/"+session.ID.String()+"/messages", string(message), 200), &reply); err != nil {
				t.Fatal(err)
			}
			var input aiclient.GenerateRequest
			select {
			case input = <-requests:
			default:
				t.Fatal("source lost before Python RAG")
			}
			if len(input.Sources) != len(tc.chunks) || len(input.Sources) > 8 {
				t.Fatal("source lost in Chat top-k/context budget")
			}
			for i, source := range input.Sources {
				chunk, ok := stored[source.ChunkID]
				if !ok || source.Content != chunk.Content || chunk.ChunkIndex != tc.chunks[i] {
					t.Fatal("wrong chunk forwarded to Python", source)
				}
			}
			if !realModel && reply.Mode != "generated" {
				t.Fatalf("protocol double did not produce a cited answer: %s", reply.Mode)
			}
			if reply.Mode == "generated" && len(reply.Sources) == 0 {
				t.Fatal("generated answer has no real citations")
			}
			for _, source := range reply.Sources {
				if !allowed[source.ChunkID] {
					t.Fatal("model cited another source", source)
				}
			}
			if realModel {
				var result aiclient.GenerateResponse
				select {
				case result = <-modelResults:
				default:
					t.Fatal("no valid Python response; fallback is not Qwen acceptance")
				}
				wantMode := map[aiclient.GenerateStatus]string{aiclient.GenerateAnswered: "generated", aiclient.GenerateNeedsClarification: "clarification", aiclient.GenerateInsufficientInformation: "no_sources"}[result.Status]
				if reply.Mode != wantMode {
					t.Fatal("Go changed the model's status", result.Status, reply.Mode)
				}
				text := strings.ToLower(reply.Message)
				switch tc.chunks[0] {
				case 10:
					// The question can mean roles or actual people. Clarification
					// is safe while the template's names are unfilled; retrieval
					// must nevertheless have delivered every real table row.
					if reply.Mode != "generated" && reply.Mode != "clarification" {
						t.Fatal("model ignored the complete role table", reply.Message)
					}
					if reply.Mode == "generated" {
						for _, role := range []string{"руководител", "секретар", "член"} {
							if !strings.Contains(text, role) {
								t.Fatal("composition incomplete", reply.Message)
							}
						}
					} else if !regexp.MustCompile(`шаблон|не\s+(заполн|указ)|нет\s+конкрет|незаполн`).MatchString(text) {
						t.Fatal("clarification did not identify the unfilled template", reply.Message)
					}
				case 16:
					if reply.Mode != "generated" || !strings.Contains(text, "организ") || !strings.Contains(text, "задач") {
						t.Fatal("wrong leader duties", reply.Message)
					}
				case 17:
					if reply.Mode != "generated" || !strings.Contains(text, "заседан") || !strings.Contains(text, "хранен") {
						t.Fatal("wrong secretary duties", reply.Message)
					}
				case 19:
					// An empty assignee cannot support a concrete-person answer.
					// Do not turn model abstention into a fake successful answer.
					if reply.Mode != "no_sources" && !regexp.MustCompile(`не\s+(указ|заполн|определ|назнач|конкретиз)|отсутств|незаполн`).MatchString(text) {
						t.Fatal("model must explicitly acknowledge the unfilled controller", reply.Message)
					}
					if strings.Contains(text, "председател") || strings.Contains(text, "заместител") {
						t.Fatal("signature mistaken for appointed controller", reply.Message)
					}
				}
				if regexp.MustCompile(`[А-ЯЁ][а-яё]+\s+[А-ЯЁ]\.\s*[А-ЯЁ]\.|\b[0-9]{1,2}[./][0-9]{1,2}[./][0-9]{2,4}\b`).MatchString(reply.Message) {
					t.Fatal("invented name/date in unfilled template", reply.Message)
				}
			}
			t.Logf("Search chunks=%v; Python input chunks=%v; Chat mode=%s; answer=%s", indexes, tc.chunks, reply.Mode, reply.Message)
		})
	}
	for _, query := range []string{
		"Как зовут контролирующего исполнение распоряжения?",
		"Иванов контролирует исполнение распоряжения?",
		"Назови фамилии участников рабочей группы",
		"Какая фамилия у секретаря рабочей группы?",
		"До какого числа рабочая группа должна завершить работу?",
	} {
		t.Run(query, func(t *testing.T) {
			member := *f.member
			member.t = t
			var session domain.ChatSession
			if err := json.Unmarshal(member.json("POST", "/api/v1/chat/sessions", `{}`, 201), &session); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{"content": query, "client_message_id": uuid.NewString()})
			data := member.json("POST", "/api/v1/chat/sessions/"+session.ID.String()+"/messages", string(body), 200)
			var reply struct {
				Mode    string
				Sources []domain.ChatMessageSource
			}
			if err := json.Unmarshal(data, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Mode != "no_sources" || len(reply.Sources) != 0 {
				t.Fatalf("unfilled fact must not become an answer: %s", data)
			}
			select {
			case <-requests:
				t.Fatal("unsupported names/dates sent to model")
			default:
			}
		})
	}
	if err := f.acl.RevokeAccess(ctx, doc.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(domain.SearchQuery{Query: workingGroupQuestions[0].query})
	var revoked struct{ Results []domain.SearchResult }
	if err := json.Unmarshal(f.member.json("POST", "/api/v1/search", string(body), 200), &revoked); err != nil {
		t.Fatal(err)
	}
	if len(revoked.Results) != 0 {
		t.Fatal("table context leaked after ACL revoke")
	}
}
