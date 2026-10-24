package aiclient

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-assistant/internal/domain"
)

func renderedFixture() []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range map[string]string{"word/document.xml": `<document/>`, "[Content_Types].xml": `<Types/>`, "_rels/.rels": `<Relationships/>`} {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(data))
	}
	_ = z.Close()
	return b.Bytes()
}
func TestTemplateClientSuccessAndFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		media  string
		body   []byte
		ok     bool
	}{
		{"success", 200, domain.DOCXMIME, renderedFixture(), true}, {"error", 500, "text/plain", []byte("private"), false},
		{"invalid_docx", 200, domain.DOCXMIME, []byte("not docx"), false}, {"empty", 200, domain.DOCXMIME, nil, false},
		{"wrong_mime", 200, "text/plain", renderedFixture(), false}, {"too_large", 200, domain.DOCXMIME, bytes.Repeat([]byte("x"), domain.MaxGeneratedBytes+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Service-Token") != "secret" || r.URL.Path != "/internal/v1/templates/render" {
					t.Error("wrong internal auth/path")
				}
				if err := r.ParseMultipartForm(6 << 20); err != nil {
					t.Error(err)
				}
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
				w.Header().Set("Content-Type", test.media)
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			client, err := New(Config{URL: server.URL, Token: "secret", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.RenderTemplate(context.Background(), []byte("input"), nil, nil)
			if (err == nil) != test.ok {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}
func TestTemplateClientCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
	}))
	defer server.Close()
	client, _ := New(Config{URL: server.URL, Token: "secret", Timeout: time.Second})
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.RenderTemplate(ctx, []byte("input"), nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.RenderTemplate(ctx, []byte("input"), nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	server.CloseClientConnections()
}
func TestTemplateClientUnavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	client, _ := New(Config{URL: endpoint, Token: "secret", Timeout: time.Second})
	defer client.Close()
	if _, err := client.RenderTemplate(context.Background(), []byte("input"), nil, nil); !errors.Is(err, domain.ErrTemplateEngine) {
		t.Fatal(err)
	}
}
