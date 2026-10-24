package aiclient

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"ai-assistant/internal/domain"
)

type TemplateInspection struct {
	Fields []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
	} `json:"fields"`
	RequiredFields []string                   `json:"required_fields"`
	MissingFields  []string                   `json:"missing_fields"`
	CanRender      bool                       `json:"can_render"`
	Validation     *domain.TemplateValidation `json:"validation,omitempty"`
}

func (c *Client) templateCall(ctx context.Context, action string, content []byte, fields []domain.TemplateField, values map[string]json.RawMessage) ([]byte, error) {
	return c.templateProfileCall(ctx, action, content, fields, nil, values)
}
func (c *Client) templateProfileCall(ctx context.Context, action string, content []byte, fields []domain.TemplateField, profile json.RawMessage, values map[string]json.RawMessage) ([]byte, error) {
	if len(content) == 0 || len(content) > domain.MaxTemplateBytes {
		return nil, domain.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("template", "template.docx")
	if err != nil {
		return nil, err
	}
	_, _ = part.Write(content)
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	data, err := json.Marshal(values)
	if err != nil || len(data) > 65536 {
		return nil, domain.ErrInvalidInput
	}
	_ = writer.WriteField("values", string(data))
	if len(profile) > 0 {
		if len(profile) > 196608 || !json.Valid(profile) {
			return nil, domain.ErrInvalidInput
		}
		_ = writer.WriteField("schema", string(profile))
	} else if fields != nil {
		data, err = json.Marshal(fields)
		if err != nil || len(data) > 65536 {
			return nil, domain.ErrInvalidInput
		}
		_ = writer.WriteField("schema", string(data))
	}
	_ = writer.Close()
	endpoint := strings.TrimSuffix(c.endpoint, "/ingest") + "/templates/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, domain.ErrTemplateEngine
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Service-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, domain.ErrTemplateEngine
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 413 {
			return nil, domain.ErrTemplateLimit
		}
		if resp.StatusCode == 422 || resp.StatusCode == 415 {
			return nil, domain.ErrInvalidInput
		}
		return nil, domain.ErrTemplateEngine
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, domain.ErrTemplateEngine
	}
	limit := int64(256 << 10)
	if action == "render" {
		limit = domain.MaxGeneratedBytes
		if media != domain.DOCXMIME {
			return nil, domain.ErrTemplateEngine
		}
	} else if media != "application/json" {
		return nil, domain.ErrTemplateEngine
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, domain.ErrTemplateEngine
	}
	if action == "render" && !validRenderedDOCX(data) {
		return nil, domain.ErrTemplateEngine
	}
	return data, nil
}
func (c *Client) InspectProfile(ctx context.Context, content []byte, profile json.RawMessage, values map[string]json.RawMessage) (TemplateInspection, error) {
	var result TemplateInspection
	data, err := c.templateProfileCall(ctx, "inspect", content, nil, profile, values)
	if err != nil {
		return result, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || result.Validation == nil || result.Validation.ProfileVersion != 2 || result.CanRender != result.Validation.Ready || len(result.Fields) > 64 {
		return result, domain.ErrTemplateEngine
	}
	var identity struct {
		ProfileID     string `json:"profile_id"`
		Revision      string `json:"revision"`
		RulesVersion  string `json:"rules_version"`
		EngineVersion string `json:"engine_version"`
	}
	if json.Unmarshal(profile, &identity) != nil || result.Validation.ProfileID != identity.ProfileID || result.Validation.ProfileRevision != identity.Revision || result.Validation.RulesVersion != identity.RulesVersion || result.Validation.EngineVersion != identity.EngineVersion {
		return result, domain.ErrTemplateEngine
	}
	return result, nil
}
func (c *Client) RenderProfile(ctx context.Context, content []byte, profile json.RawMessage, values map[string]json.RawMessage) ([]byte, error) {
	return c.templateProfileCall(ctx, "render", content, nil, profile, values)
}
func (c *Client) InspectTemplate(ctx context.Context, content []byte, fields []domain.TemplateField, values map[string]json.RawMessage) (TemplateInspection, error) {
	var result TemplateInspection
	data, err := c.templateCall(ctx, "inspect", content, fields, values)
	if err != nil {
		return result, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || len(result.Fields) > 64 {
		return result, domain.ErrTemplateEngine
	}
	seen := map[string]bool{}
	for _, f := range result.Fields {
		if f.Name == "" || seen[f.Name] || f.Type != "string" {
			return result, domain.ErrTemplateEngine
		}
		seen[f.Name] = true
	}
	return result, nil
}
func (c *Client) RenderTemplate(ctx context.Context, content []byte, fields []domain.TemplateField, values map[string]json.RawMessage) ([]byte, error) {
	return c.templateCall(ctx, "render", content, fields, values)
}

func validRenderedDOCX(data []byte) bool {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) > 1000 {
		return false
	}
	seen := map[string]bool{}
	var expanded uint64
	for _, entry := range z.File {
		if seen[entry.Name] || entry.UncompressedSize64 > 32<<20 || expanded > 32<<20-entry.UncompressedSize64 {
			return false
		}
		seen[entry.Name] = true
		expanded += entry.UncompressedSize64
		if entry.Name == "word/document.xml" || entry.Name == "[Content_Types].xml" {
			r, err := entry.Open()
			if err != nil {
				return false
			}
			dec := xml.NewDecoder(r)
			tokens := 0
			for {
				_, err = dec.Token()
				if err != nil {
					break
				}
				tokens++
			}
			closeErr := r.Close()
			if !errors.Is(err, io.EOF) || closeErr != nil || tokens == 0 {
				return false
			}
		}
	}
	return seen["word/document.xml"] && seen["[Content_Types].xml"] && seen["_rels/.rels"]
}
