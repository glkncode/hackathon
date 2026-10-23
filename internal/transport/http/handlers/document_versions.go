package handlers

import (
	"errors"
	"net/http"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
)

func (h *DocumentHandler) UploadVersion(w http.ResponseWriter, r *http.Request) {
	documentID, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes+(1<<20))
	err = r.ParseMultipartForm(1 << 20)
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
	// No version_number, status, storage key or activation flags from clients.
	if form == nil || len(form.Value) != 0 || len(form.File) != 1 || len(form.File["file"]) != 1 {
		apiError(w, domain.ErrInvalidInput, h.log)
		return
	}
	header := form.File["file"][0]
	if header.Size > h.maxBytes {
		apiError(w, service.ErrFileTooLarge, h.log)
		return
	}
	file, err := header.Open()
	if err != nil {
		apiError(w, errors.New("open uploaded version failed"), h.log)
		return
	}
	defer file.Close()
	version, err := h.documents.UploadNewVersion(r.Context(), actor(r), documentID, header.Filename, file)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (h *DocumentHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	documentID, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	limit, offset, err := pagination(r)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	versions, total, err := h.documents.ListVersions(r.Context(), actor(r), documentID, limit, offset)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []domain.DocumentVersion `json:"items"`
		Total int64                    `json:"total"`
	}{versions, total})
}

func (h *DocumentHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	documentID, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	versionID, err := pathID(r, "versionId")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	version, err := h.documents.GetVersion(r.Context(), actor(r), documentID, versionID)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (h *DocumentHandler) DownloadVersion(w http.ResponseWriter, r *http.Request) {
	documentID, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	versionID, err := pathID(r, "versionId")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	version, file, err := h.documents.DownloadVersion(r.Context(), actor(r), documentID, versionID)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	defer file.Close()
	serveVersion(w, r, version, file)
}
