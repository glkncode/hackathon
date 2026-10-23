package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

func docxBytes(t *testing.T, extra string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	files := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`,
	}
	if extra != "" {
		files[extra] = "bad entry"
	}
	for name, contents := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestLocalDocumentFormatsAndCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalDocumentStorage(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		name string
		data []byte
		mime string
	}{
		{"policy.PDF", []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n"), "application/pdf"},
		{"policy.docx", docxBytes(t, ""), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"policy.txt", []byte("Инструкция\nHello\tworld"), "text/plain; charset=utf-8"},
	} {
		f, err := s.Save(context.Background(), bytes.NewReader(tc.data), tc.name)
		if err != nil || f.MIMEType != tc.mime || f.Size != int64(len(tc.data)) || !storageKey(f.Key) {
			t.Fatalf("%s: %+v %v", tc.name, f, err)
		}
		digest := sha256.Sum256(tc.data)
		if f.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("checksum does not match original bytes")
		}
		opened, err := s.Open(f.Key)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := io.ReadAll(opened)
		opened.Close()
		if err != nil || !bytes.Equal(loaded, tc.data) {
			t.Fatal("original changed")
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(filepath.Join(dir, f.Key))
			if info.Mode().Perm() != 0600 {
				t.Fatal("file is not private")
			}
		}
		if err := s.Delete(f.Key); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"fake.pdf", []byte("plain text")}, {"fake.docx", []byte("PK fake zip")},
		{"path.docx", docxBytes(t, "../escape.txt")}, {"binary.txt", []byte{0, 1, 2}},
		{"invalid.txt", []byte{0xff, 0xfe}}, {"empty.txt", nil}, {"file.exe", []byte("program")},
		{"bad\r\n.txt", []byte("text")}, {"large.txt", bytes.Repeat([]byte("a"), 4097)},
	} {
		if _, err := s.Save(context.Background(), bytes.NewReader(tc.data), tc.name); err == nil {
			t.Fatalf("invalid file %q accepted", tc.name)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("failed upload left files: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Save(ctx, strings.NewReader("text"), "cancelled.txt"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("cancelled upload left file")
	}
}
func TestLocalDocumentTraversal(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocalDocumentStorage(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"../../escape.txt", `..\..\escape.txt`, `C:\windows\escape.txt`} {
		f, err := s.Save(context.Background(), strings.NewReader("text"), name)
		if err != nil || f.Filename != "escape.txt" || !storageKey(f.Key) {
			t.Fatalf("unsafe filename: %+v %v", f, err)
		}
		if _, err := os.Stat(filepath.Join(dir, f.Key)); err != nil {
			t.Fatal("file escaped storage")
		}
	}
	for _, key := range []string{"../outside", "/etc/passwd", `..\outside`, uuid.NewString() + "/../outside"} {
		if _, err := s.Open(key); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("unsafe key accepted")
		}
		if err := s.Delete(key); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("unsafe delete accepted")
		}
	}
}
func TestLocalDocumentSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if err := os.Symlink(outside, filepath.Join(dir, key)); err != nil {
		t.Skip("symlink creation not available on this host")
	}
	s, err := NewLocalDocumentStorage(dir, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if f, err := s.Open(key); err == nil {
		f.Close()
		t.Fatal("symlink escaped storage")
	}
}
