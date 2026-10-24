// Package aiclient implements the private Go-to-Python ingestion and RAG contracts.
package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type Config struct {
	URL, Token       string
	Timeout          time.Duration
	GenerateTimeout  time.Duration // Zero uses Timeout; shares the same transport.
	EmbeddingTimeout time.Duration
}
type Client struct {
	endpoint, token                string
	generateEndpoint               string
	ingestTimeout, generateTimeout time.Duration
	embeddingTimeout               time.Duration
	http                           *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.EmbeddingTimeout == 0 {
		cfg.EmbeddingTimeout = 30 * time.Second
	}
	if cfg.EmbeddingTimeout < time.Second || cfg.EmbeddingTimeout > time.Hour {
		return nil, errors.New("AI client: invalid embedding timeout")
	}
	if cfg.GenerateTimeout == 0 {
		cfg.GenerateTimeout = cfg.Timeout
	}
	if cfg.GenerateTimeout < time.Second || cfg.GenerateTimeout > time.Hour {
		return nil, errors.New("AI client: invalid generation timeout")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" || cfg.Token == "" || strings.ContainsAny(cfg.Token, "\r\n") || cfg.Timeout < time.Second || cfg.Timeout > time.Hour {
		return nil, errors.New("AI client: invalid server configuration")
	}
	u.Path = "/internal/v1/ingest"
	ingestEndpoint := u.String()
	u.Path = "/internal/v1/rag/generate"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Private files and credentials must not follow ambient proxies.
	return &Client{endpoint: ingestEndpoint, generateEndpoint: u.String(), token: cfg.Token, ingestTimeout: cfg.Timeout, generateTimeout: cfg.GenerateTimeout, embeddingTimeout: cfg.EmbeddingTimeout, http: &http.Client{Timeout: max(cfg.Timeout, cfg.GenerateTimeout, cfg.EmbeddingTimeout), Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Ingest(ctx context.Context, input Request) (Response, error) {
	if input.File == nil {
		return Response{}, &Error{Code: "source_unavailable"}
	}
	defer input.File.Close()
	if input.DocumentID == uuid.Nil || input.DocumentVersionID == uuid.Nil || input.MIMEType == "" {
		return Response{}, &Error{Code: "invalid_ai_response"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.ingestTimeout)
	defer cancel()
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, reader)
	if err != nil {
		reader.Close()
		writer.Close()
		return Response{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("X-Service-Token", c.token)
	req.Header.Set("Accept", "application/json")
	stop := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()); input.File.Close() })
	defer stop()
	produced := make(chan error, 1)
	go func() {
		var err error
		for _, field := range [][2]string{{"document_id", input.DocumentID.String()}, {"document_version_id", input.DocumentVersionID.String()}, {"mime_type", input.MIMEType}} {
			if err = multipartWriter.WriteField(field[0], field[1]); err != nil {
				break
			}
		}
		if err == nil {
			var part io.Writer
			part, err = multipartWriter.CreateFormFile("file", input.Filename)
			if err == nil {
				_, err = io.Copy(part, input.File)
			}
		}
		if err == nil {
			err = multipartWriter.Close()
		}
		writer.CloseWithError(err)
		produced <- err
	}()
	resp, err := c.http.Do(req)
	// Also stop a producer when the peer responds before consuming the upload.
	reader.Close()
	input.File.Close()
	uploadErr := <-produced
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Response{}, &Error{Code: "ai_timeout", Temporary: true}
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
		return Response{}, &Error{Code: "ai_rejected", Temporary: retry}
	}
	if uploadErr != nil {
		return Response{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return Response{}, &Error{Code: "invalid_ai_response"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, &Error{Code: "ai_unavailable", Temporary: true}
	}
	if len(body) > MaxResponseBytes || !utf8.Valid(body) {
		return Response{}, &Error{Code: "invalid_ai_response"}
	}
	return decode(body, input.DocumentVersionID)
}

func decode(body []byte, versionID uuid.UUID) (Response, error) {
	invalid := &Error{Code: "invalid_ai_response"}
	var wire struct {
		DocumentVersionID uuid.UUID `json:"document_version_id"`
		Chunks            []struct {
			Index   *int    `json:"chunk_index"`
			Content *string `json:"content"`
			Page    *int    `json:"page_number"`
			Number  *string `json:"section_number"`
			Title   *string `json:"section_title"`
		} `json:"chunks"`
		Warnings []string `json:"warnings"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.DocumentVersionID != versionID || len(wire.Warnings) > 100 {
		return Response{}, invalid
	}
	result := Response{DocumentVersionID: versionID, Warnings: wire.Warnings}
	for _, c := range wire.Chunks {
		if c.Index == nil || c.Content == nil {
			return Response{}, invalid
		}
		result.Chunks = append(result.Chunks, domain.DocumentChunk{DocumentVersionID: versionID, ChunkIndex: *c.Index, Content: *c.Content, PageNumber: c.Page, SectionNumber: c.Number, SectionTitle: c.Title})
	}
	if domain.ValidateChunks(result.Chunks) != nil {
		return Response{}, invalid
	}
	for _, w := range wire.Warnings {
		if len(w) > 1024 || strings.ContainsRune(w, 0) {
			return Response{}, invalid
		}
	}
	return result, nil
}
