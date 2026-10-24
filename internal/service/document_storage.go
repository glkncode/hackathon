package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

var ErrFileTooLarge = errors.New("document exceeds upload limit")

type StoredFile struct {
	Key      string
	Filename string
	MIMEType string
	Size     int64
	SHA256   string
}
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}
type DocumentStorage interface {
	Save(context.Context, io.Reader, string) (StoredFile, error)
	Open(string) (ReadSeekCloser, error)
	Delete(string) error
}

// LocalDocumentStorage owns a confined directory handle, never a public URL.
// os.Root prevents traversal and symlink escapes even during concurrent changes.
type LocalDocumentStorage struct {
	root     *os.Root
	maxBytes int64
}

func NewLocalDocumentStorage(directory string, maxBytes int64) (*LocalDocumentStorage, error) {
	if strings.TrimSpace(directory) == "" || maxBytes < 1 || maxBytes > 1<<30 {
		return nil, domain.ErrInvalidInput
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("create document storage directory failed")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("document storage must be a real directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("open document storage failed")
	}
	if err := protectDocumentDirectory(root); err != nil {
		root.Close()
		return nil, errors.New("protect document storage failed")
	}
	return &LocalDocumentStorage{root: root, maxBytes: maxBytes}, nil
}
func (s *LocalDocumentStorage) Close() error { return s.root.Close() }

func storageKey(key string) bool {
	id, err := uuid.Parse(key)
	return err == nil && id != uuid.Nil && id.String() == key
}
func (s *LocalDocumentStorage) Open(key string) (ReadSeekCloser, error) {
	if !storageKey(key) {
		return nil, domain.ErrNotFound
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, errors.New("open stored document failed")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("invalid stored document")
	}
	return f, nil
}
func (s *LocalDocumentStorage) Delete(key string) error {
	if !storageKey(key) {
		return domain.ErrInvalidInput
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("delete stored document failed")
	}
	return nil
}

func safeFilename(name string) (string, error) {
	// Browsers may send Windows paths even when the server runs on Linux.
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || name == "" || len(name) > 255 || !utf8.ValidString(name) {
		return "", domain.ErrInvalidInput
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", domain.ErrInvalidInput
		}
	}
	return name, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *LocalDocumentStorage) Save(ctx context.Context, reader io.Reader, filename string) (stored StoredFile, err error) {
	filename, err = safeFilename(filename)
	if err != nil {
		return stored, err
	}
	ext := strings.ToLower(path.Ext(filename))
	if ext != ".pdf" && ext != ".docx" && ext != ".txt" {
		return stored, domain.ErrInvalidInput
	}
	key := uuid.NewString()
	staging := "." + key + ".tmp"
	f, err := s.root.OpenFile(staging, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return stored, errors.New("create stored document failed")
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			_ = s.root.Remove(staging)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(contextReader{ctx: ctx, reader: reader}, s.maxBytes+1))
	if err != nil {
		return stored, err
	}
	if size > s.maxBytes {
		return stored, ErrFileTooLarge
	}
	if size == 0 {
		return stored, domain.ErrInvalidInput
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return stored, errors.New("read uploaded document failed")
	}
	mimeType, err := validateFile(f, size, ext)
	if err != nil {
		return stored, err
	}
	if err := ctx.Err(); err != nil {
		return stored, err
	}
	if err := f.Sync(); err != nil {
		return stored, errors.New("sync stored document failed")
	}
	if err := f.Close(); err != nil {
		return stored, errors.New("close stored document failed")
	}
	if err := s.root.Rename(staging, key); err != nil {
		return stored, errors.New("publish stored document failed")
	}
	complete = true
	return StoredFile{Key: key, Filename: filename, MIMEType: mimeType, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// These checks identify formats; they do not extract content or guarantee that
// a PDF/Office document is harmless. Downloads are always attachments.
func validateFile(f *os.File, size int64, ext string) (string, error) {
	switch ext {
	case ".pdf":
		header := make([]byte, 8)
		if _, err := f.ReadAt(header, 0); err != nil || !bytes.HasPrefix(header, []byte("%PDF-")) || header[5] < '1' || header[5] > '2' || header[6] != '.' || header[7] < '0' || header[7] > '9' {
			return "", domain.ErrInvalidInput
		}
		tail := make([]byte, min(size, 1024))
		if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil || !bytes.HasSuffix(bytes.TrimSpace(tail), []byte("%%EOF")) {
			return "", domain.ErrInvalidInput
		}
		return "application/pdf", nil
	case ".docx":
		z, err := zip.NewReader(f, size)
		if err != nil || len(z.File) > 2048 {
			return "", domain.ErrInvalidInput
		}
		var types, document *zip.File
		seen := make(map[string]bool)
		var expanded uint64
		for _, entry := range z.File {
			if strings.Contains(entry.Name, `\`) || strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, ":") || path.Clean(entry.Name) != strings.TrimSuffix(entry.Name, "/") || entry.Name == ".." || strings.HasPrefix(entry.Name, "../") || seen[entry.Name] {
				return "", domain.ErrInvalidInput
			}
			seen[entry.Name] = true
			if entry.UncompressedSize64 > 100<<20 || expanded > 100<<20-entry.UncompressedSize64 {
				return "", domain.ErrInvalidInput
			}
			expanded += entry.UncompressedSize64
			if entry.Name == "[Content_Types].xml" {
				types = entry
			}
			if entry.Name == "word/document.xml" {
				document = entry
			}
		}
		if types == nil || document == nil || document.UncompressedSize64 == 0 || types.UncompressedSize64 > 64<<10 {
			return "", domain.ErrInvalidInput
		}
		documentReader, err := document.Open()
		if err != nil {
			return "", domain.ErrInvalidInput
		}
		defer documentReader.Close()
		// Only inspect the XML root to identify WordprocessingML; no document
		// text is parsed, extracted or indexed at this stage.
		docDecoder := xml.NewDecoder(io.LimitReader(documentReader, 64<<10))
		for {
			token, err := docDecoder.Token()
			if err != nil {
				return "", domain.ErrInvalidInput
			}
			if start, ok := token.(xml.StartElement); ok {
				if start.Name.Local != "document" || start.Name.Space != "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					return "", domain.ErrInvalidInput
				}
				break
			}
		}
		rc, err := types.Open()
		if err != nil {
			return "", domain.ErrInvalidInput
		}
		defer rc.Close()
		var ct struct {
			XMLName   xml.Name `xml:"http://schemas.openxmlformats.org/package/2006/content-types Types"`
			Overrides []struct {
				Part string `xml:"PartName,attr"`
				Type string `xml:"ContentType,attr"`
			} `xml:"Override"`
		}
		decoder := xml.NewDecoder(io.LimitReader(rc, 64<<10+1))
		if err := decoder.Decode(&ct); err != nil {
			return "", domain.ErrInvalidInput
		}
		// Drain to check ZIP CRC and reject data following the XML document.
		for {
			tok, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", domain.ErrInvalidInput
			}
			if data, ok := tok.(xml.CharData); !ok || len(bytes.TrimSpace(data)) != 0 {
				return "", domain.ErrInvalidInput
			}
		}
		for _, o := range ct.Overrides {
			if o.Part == "/word/document.xml" && o.Type == "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml" {
				return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
			}
		}
	case ".txt":
		r := bufio.NewReader(f)
		for {
			ch, n, err := r.ReadRune()
			if err == io.EOF {
				return "text/plain; charset=utf-8", nil
			}
			if err != nil || (ch == utf8.RuneError && n == 1) || (unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t') {
				return "", domain.ErrInvalidInput
			}
		}
	}
	return "", domain.ErrInvalidInput
}
