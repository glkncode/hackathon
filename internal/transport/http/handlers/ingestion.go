package handlers

import (
	"net/http"

	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

type IngestionHandler struct {
	service *service.IngestionService
	log     *zap.Logger
}

func NewIngestion(s *service.IngestionService, log *zap.Logger) *IngestionHandler {
	return &IngestionHandler{s, log}
}
func (h *IngestionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	job, err := h.service.Get(r.Context(), actor(r), id)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, job)
}
func (h *IngestionHandler) Retry(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	job, err := h.service.Retry(r.Context(), actor(r), id)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, job)
}
