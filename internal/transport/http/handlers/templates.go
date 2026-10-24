package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TemplateHandler struct {
	templates *service.TemplateService
	log       *zap.Logger
}

func NewTemplates(s *service.TemplateService, log *zap.Logger) *TemplateHandler {
	return &TemplateHandler{templates: s, log: log}
}
func readTemplateJSON(w http.ResponseWriter, r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return domain.ErrInvalidInput
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err = dec.Decode(target); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return service.ErrFileTooLarge
		}
		return domain.ErrInvalidInput
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return service.ErrFileTooLarge
		}
		return domain.ErrInvalidInput
	}
	return nil
}
func (h *TemplateHandler) fail(w http.ResponseWriter, err error) {
	var validation domain.TemplateValidation
	switch {
	case errors.As(err, &validation):
		writeJSON(w, 422, validation)
	case errors.Is(err, domain.ErrRevisionConflict):
		writeJSON(w, 409, map[string]string{"error": "draft revision or question conflict"})
	case errors.Is(err, domain.ErrTemplateBusy):
		writeJSON(w, 503, map[string]string{"error": "template generation capacity reached"})
	case errors.Is(err, domain.ErrTemplateLimit):
		writeJSON(w, 413, map[string]string{"error": "template resource limit exceeded"})
	case errors.Is(err, domain.ErrTemplateNeedsConfiguration):
		writeJSON(w, 400, map[string]string{"error": "template needs configuration", "support_status": "needs_configuration"})
	case errors.Is(err, domain.ErrGenerationCommitUnknown):
		writeJSON(w, 503, map[string]string{"error": "generation outcome uncertain; retry the same draft revision"})
	case service.TemplateEngineFailure(err):
		writeJSON(w, 502, map[string]string{"error": "template engine unavailable"})
	default:
		apiError(w, err, h.log)
	}
}

// Handle binds a fixed operation chosen by the router, never by request input.
func (h *TemplateHandler) Handle(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var result any
		var err error
		status := 200
		actor := actor(r)
		id := uuid.Nil
		if r.PathValue("id") != "" {
			id, err = pathID(r, "id")
			if err != nil {
				h.fail(w, err)
				return
			}
		}
		switch operation {
		case "register":
			var input struct {
				DocumentID        uuid.UUID              `json:"document_id"`
				DocumentVersionID uuid.UUID              `json:"document_version_id"`
				Name              string                 `json:"name"`
				Description       string                 `json:"description"`
				Fields            []domain.TemplateField `json:"fields"`
				Profile           json.RawMessage        `json:"profile"`
			}
			if err = readTemplateJSON(w, r, &input); err == nil {
				result, err = h.templates.Register(r.Context(), actor, domain.DocumentTemplate{DocumentID: input.DocumentID, DocumentVersionID: input.DocumentVersionID, Name: input.Name, Description: input.Description, Fields: input.Fields, Profile: input.Profile})
			}
			status = 201
		case "list", "drafts":
			var limit, offset int
			limit, offset, err = pagination(r)
			if err == nil {
				var items any
				if operation == "list" {
					items, err = h.templates.ListTemplates(r.Context(), actor, limit, offset)
				} else {
					items, err = h.templates.ListDrafts(r.Context(), actor, limit, offset)
				}
				result = map[string]any{"items": items, "limit": limit, "offset": offset}
			}
		case "template":
			result, err = h.templates.GetTemplate(r.Context(), actor, id)
		case "enabled":
			var input struct {
				Enabled *bool `json:"enabled"`
			}
			if err = readTemplateJSON(w, r, &input); err == nil {
				if input.Enabled == nil {
					err = domain.ErrInvalidInput
				} else {
					result, err = h.templates.SetEnabled(r.Context(), actor, id, *input.Enabled)
				}
			}
		case "create":
			result, err = h.templates.CreateDraft(r.Context(), actor, id)
			status = 201
		case "draft":
			result, err = h.templates.GetDraft(r.Context(), actor, id)
		case "update":
			var input struct {
				ExpectedRevision int64                      `json:"expected_revision"`
				Values           map[string]json.RawMessage `json:"values"`
			}
			if err = readTemplateJSON(w, r, &input); err == nil {
				result, err = h.templates.UpdateDraftFields(r.Context(), actor, id, input.ExpectedRevision, input.Values)
			}
		case "answer":
			var input struct {
				ExpectedRevision int64           `json:"expected_revision"`
				FieldKey         string          `json:"field_key"`
				Value            json.RawMessage `json:"value"`
			}
			if err = readTemplateJSON(w, r, &input); err == nil {
				result, err = h.templates.SubmitAnswer(r.Context(), actor, id, input.ExpectedRevision, input.FieldKey, input.Value)
			}
		case "validate", "next":
			result, err = h.templates.ValidateDraft(r.Context(), actor, id)
		case "generate":
			var input struct {
				ExpectedRevision int64 `json:"expected_revision"`
			}
			if err = readTemplateJSON(w, r, &input); err == nil {
				var g domain.GeneratedDocument
				g, err = h.templates.GenerateDocument(r.Context(), actor, id, input.ExpectedRevision)
				result = map[string]any{"generated_document_id": g.ID, "draft_id": g.DraftID, "draft_revision": g.DraftRevision, "status": "ready", "filename": g.Filename, "download_url": "/api/v1/generated-documents/" + g.ID.String() + "/download"}
				if len(g.GenerationSnapshot) > 0 {
					result.(map[string]any)["generation_snapshot"] = g.GenerationSnapshot
				}
			}
		case "generated":
			result, err = h.templates.GetGeneratedDocument(r.Context(), actor, id)
		case "download":
			var g domain.GeneratedDocument
			var file service.ReadSeekCloser
			g, file, err = h.templates.DownloadGeneratedDocument(r.Context(), actor, id)
			if err == nil {
				defer file.Close()
				serveVersion(w, r, domain.DocumentVersion{MIMEType: g.MIMEType, OriginalFilename: g.Filename, SizeBytes: g.SizeBytes, CreatedAt: g.CreatedAt}, file)
				return
			}
		default:
			err = domain.ErrNotFound
		}
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, status, result)
	}
}
