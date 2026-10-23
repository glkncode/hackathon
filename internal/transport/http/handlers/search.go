package handlers

import (
	"net/http"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"go.uber.org/zap"
)

type SearchHandler struct {
	service *service.SearchService
	log     *zap.Logger
}

func NewSearch(search *service.SearchService, log *zap.Logger) *SearchHandler {
	return &SearchHandler{service: search, log: log}
}

func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	var query domain.SearchQuery
	if err := readJSON(w, r, &query); err != nil {
		apiError(w, err, h.log)
		return
	}
	results, err := h.service.Search(r.Context(), actor(r), query)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	// The service validated the text. Return the normalized query; total counts
	// this bounded result slice, not every matching row in the database.
	query, _ = query.Normalize()
	writeJSON(w, http.StatusOK, struct {
		Query   string                `json:"query"`
		Mode    string                `json:"mode"`
		Results []domain.SearchResult `json:"results"`
		Total   int                   `json:"total"`
	}{Query: query.Query, Mode: "search", Results: results, Total: len(results)})
}
