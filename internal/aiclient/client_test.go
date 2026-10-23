package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

func request(version uuid.UUID) Request {
	return Request{DocumentID: uuid.New(), DocumentVersionID: version, MIMEType: "text/plain", Filename: "private.txt", File: io.NopCloser(strings.NewReader("private source"))}
}
func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(Config{URL: url, Token: "secret-service-token", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func validBody(version uuid.UUID) string {
	return fmt.Sprintf(`{"document_version_id":%q,"chunks":[{"chunk_index":0,"content":"Правила согласования","page_number":5,"section_number":"2.3","section_title":null}],"warnings":[]}`, version)
}
func TestStreamingContract(t *testing.T) {
	version := uuid.New()
	input := request(version)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/internal/v1/ingest" || r.Header.Get("X-Service-Token") != "secret-service-token" {
			t.Error("incorrect private contract")
		}
		if r.ContentLength != -1 {
			t.Error("upload should be streamed")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("document_id") != input.DocumentID.String() || r.FormValue("document_version_id") != version.String() || r.FormValue("mime_type") != "text/plain" {
			t.Error("incorrect multipart fields")
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		body, _ := io.ReadAll(f)
		if string(body) != "private source" || h.Filename != "private.txt" {
			t.Error("file mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validBody(version))
	}))
	defer server.Close()
	result, err := newTestClient(t, server.URL).Ingest(context.Background(), input)
	if err != nil || len(result.Chunks) != 1 || result.Chunks[0].DocumentVersionID != version || *result.Chunks[0].PageNumber != 5 {
		t.Fatalf("result: %+v %v", result, err)
	}
}

func TestRejectInvalidResponses(t *testing.T) {
	version := uuid.New()
	bodies := map[string]string{
		"wrong version":   validBody(uuid.New()),
		"empty":           fmt.Sprintf(`{"document_version_id":%q,"chunks":[]}`, version),
		"missing index":   fmt.Sprintf(`{"document_version_id":%q,"chunks":[{"content":"x"}]}`, version),
		"missing content": fmt.Sprintf(`{"document_version_id":%q,"chunks":[{"chunk_index":0}]}`, version),
		"blank":           fmt.Sprintf(`{"document_version_id":%q,"chunks":[{"chunk_index":0,"content":"  "}]}`, version),
		"duplicates":      fmt.Sprintf(`{"document_version_id":%q,"chunks":[{"chunk_index":0,"content":"x"},{"chunk_index":0,"content":"y"}]}`, version),
		"negative index":  strings.Replace(validBody(version), `"chunk_index":0`, `"chunk_index":-1`, 1),
		"zero page":       strings.Replace(validBody(version), `"page_number":5`, `"page_number":0`, 1),
		"huge page":       strings.Replace(validBody(version), `"page_number":5`, `"page_number":1000001`, 1),
		"unknown":         strings.Replace(validBody(version), `"warnings":[]`, `"unexpected":true`, 1),
		"trailing":        validBody(version) + ` {}`,
		"malformed":       `{"private source":`,
		"oversize chunk":  strings.Replace(validBody(version), "Правила согласования", strings.Repeat("x", domain.MaxChunkBytes+1), 1),
		"body cap":        strings.Repeat(" ", MaxResponseBytes+1),
		"NUL":             strings.Replace(validBody(version), "Правила согласования", `bad\u0000text`, 1),
	}
	many := make([]map[string]any, domain.MaxChunks+1)
	for i := range many {
		many[i] = map[string]any{"chunk_index": i, "content": "x"}
	}
	raw, _ := json.Marshal(map[string]any{"document_version_id": version, "chunks": many})
	bodies["too many chunks"] = string(raw)
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Ingest(context.Background(), request(version))
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != "invalid_ai_response" || remote.Temporary {
				t.Fatalf("expected safe permanent validation error, got %v", err)
			}
			if strings.Contains(err.Error(), "private source") || strings.Contains(err.Error(), "secret-service-token") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("data leaked")
			}
		})
	}
}

func TestHTTPFailuresAndRedirects(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 413, 422, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				io.WriteString(w, "secret-service-token private source")
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Ingest(context.Background(), request(uuid.New()))
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != "ai_rejected" || remote.Temporary != (status == 408 || status == 429 || status >= 500) {
				t.Fatalf("classification: %v", err)
			}
		})
	}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		http.Redirect(w, r, target.URL, 307)
	}))
	defer redirect.Close()
	_, err := newTestClient(t, redirect.URL).Ingest(context.Background(), request(uuid.New()))
	if err == nil || hits.Load() != 0 {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestCancellationTimeoutAndNetwork(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			client := newTestClient(t, server.URL)
			go func() { _, err := client.Ingest(ctx, request(uuid.New())); result <- err }()
			<-started
			if !timeout {
				cancel()
			}
			select {
			case err := <-result:
				if timeout {
					var remote *Error
					if !errors.As(err, &remote) || remote.Code != "ai_timeout" {
						t.Fatalf("timeout=%v", err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client did not stop")
			}
		})
	}
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	_, err := newTestClient(t, endpoint).Ingest(context.Background(), request(uuid.New()))
	var remote *Error
	if !errors.As(err, &remote) || !remote.Temporary || remote.Code != "ai_unavailable" {
		t.Fatalf("network=%v", err)
	}
}

type stalledFile struct {
	closed  chan struct{}
	reading chan struct{}
	once    sync.Once
}

func (f *stalledFile) Read([]byte) (int, error) {
	close(f.reading)
	<-f.closed
	return 0, io.ErrClosedPipe
}
func (f *stalledFile) Close() error { f.once.Do(func() { close(f.closed) }); return nil }
func TestCancelDuringStreamingUploadClosesSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) }))
	defer server.Close()
	client := newTestClient(t, server.URL)
	file := &stalledFile{closed: make(chan struct{}), reading: make(chan struct{})}
	input := request(uuid.New())
	input.File = file
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Ingest(ctx, input); done <- err }()
	<-file.reading
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("streaming producer leaked")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", ":bad", "ftp://localhost", "http://user:pass@localhost", "http://localhost/path", "http://localhost?token=secret", "http://localhost/#secret"} {
		if _, err := New(Config{URL: endpoint, Token: "secret", Timeout: time.Second}); err == nil || strings.Contains(err.Error(), "pass") {
			t.Fatal("invalid configuration accepted or leaked")
		}
	}
}
