package handlers

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Documents interface {
	Create(context.Context, domain.User, string, string, io.Reader) (service.DocumentDetails, error)
	Get(context.Context, domain.User, uuid.UUID) (service.DocumentDetails, error)
	List(context.Context, domain.User, int, int) ([]domain.Document, int64, error)
	Download(context.Context, domain.User, uuid.UUID) (service.DocumentDetails, service.ReadSeekCloser, error)
	UploadNewVersion(context.Context, domain.User, uuid.UUID, string, io.Reader) (domain.DocumentVersion, error)
	ListVersions(context.Context, domain.User, uuid.UUID, int, int) ([]domain.DocumentVersion, int64, error)
	GetVersion(context.Context, domain.User, uuid.UUID, uuid.UUID) (domain.DocumentVersion, error)
	DownloadVersion(context.Context, domain.User, uuid.UUID, uuid.UUID) (domain.DocumentVersion, service.ReadSeekCloser, error)
}
type DocumentAccess interface {
	Grant(context.Context, domain.User, uuid.UUID, uuid.UUID) (domain.DocumentAccess, error)
	Revoke(context.Context, domain.User, uuid.UUID, uuid.UUID) error
	List(context.Context, domain.User, uuid.UUID, int, int) ([]domain.User, int64, error)
}
type DocumentHandler struct {
	documents Documents
	access    DocumentAccess
	maxBytes  int64
	log       *zap.Logger
}

func NewDocuments(documents Documents, access DocumentAccess, maxBytes int64, log *zap.Logger) *DocumentHandler {
	return &DocumentHandler{documents: documents, access: access, maxBytes: maxBytes, log: log}
}
func (h *DocumentHandler) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes+(1<<20))
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apiError(w, service.ErrFileTooLarge, h.log)
		} else {
			apiError(w, domain.ErrInvalidInput, h.log)
		}
		return
	}
	form := r.MultipartForm
	if form == nil || len(form.Value) != 1 || len(form.Value["title"]) != 1 || len(form.File) != 1 || len(form.File["file"]) != 1 {
		apiError(w, domain.ErrInvalidInput, h.log)
		return
	}
	header := form.File["file"][0]
	if header.Size > h.maxBytes {
		apiError(w, service.ErrFileTooLarge, h.log)
		return
	}
	f, err := header.Open()
	if err != nil {
		apiError(w, errors.New("open uploaded file failed"), h.log)
		return
	}
	defer f.Close()
	details, err := h.documents.Create(r.Context(), actor(r), form.Value["title"][0], header.Filename, f)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 201, details)
}
func (h *DocumentHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	items, total, err := h.documents.List(r.Context(), actor(r), limit, offset)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 200, struct {
		Items []domain.Document `json:"items"`
		Total int64             `json:"total"`
	}{items, total})
}
func (h *DocumentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	details, err := h.documents.Get(r.Context(), actor(r), id)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 200, details)
}
func (h *DocumentHandler) Download(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	details, file, err := h.documents.Download(r.Context(), actor(r), id)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	defer file.Close()
	serveVersion(w, r, details.ActiveVersion, file)
}

func serveVersion(w http.ResponseWriter, r *http.Request, version domain.DocumentVersion, file service.ReadSeekCloser) {
	w.Header().Set("Content-Type", version.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": version.OriginalFilename}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "", version.CreatedAt, file)
}
func (h *DocumentHandler) Grant(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	var input struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := readJSON(w, r, &input); err != nil || input.UserID == uuid.Nil {
		apiError(w, domain.ErrInvalidInput, h.log)
		return
	}
	access, err := h.access.Grant(r.Context(), actor(r), id, input.UserID)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 201, access)
}
func (h *DocumentHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	userID, err := pathID(r, "userId")
	if err != nil {
		apiError(w, domain.ErrInvalidInput, h.log)
		return
	}
	if err := h.access.Revoke(r.Context(), actor(r), id, userID); err != nil {
		apiError(w, err, h.log)
		return
	}
	w.WriteHeader(204)
}
func (h *DocumentHandler) ListAccess(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	limit, offset, err := pagination(r)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	users, total, err := h.access.List(r.Context(), actor(r), id, limit, offset)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 200, struct {
		Items []listedUser `json:"items"`
		Total int64        `json:"total"`
	}{listedUsers(users), total})
}
