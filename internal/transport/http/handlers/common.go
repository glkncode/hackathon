package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"ai-assistant/internal/transport/http/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type listedUser struct {
	ID       uuid.UUID   `json:"id"`
	Username string      `json:"username"`
	Role     domain.Role `json:"role"`
	IsActive bool        `json:"is_active"`
}

func listedUsers(users []domain.User) []listedUser {
	items := make([]listedUser, 0, len(users))
	for _, u := range users {
		items = append(items, listedUser{ID: u.ID, Username: u.Username, Role: u.Role, IsActive: u.IsActive})
	}
	return items
}
func actor(r *http.Request) domain.User {
	identity, _ := middleware.Current(r.Context())
	return identity.User
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return domain.ErrInvalidInput
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.ErrInvalidInput
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return domain.ErrInvalidInput
	}
	return nil
}
func pagination(r *http.Request) (int, int, error) {
	limit, offset := 20, 0
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, domain.ErrInvalidInput
	}
	for _, p := range []struct {
		name   string
		target *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if values, ok := query[p.name]; ok {
			if len(values) != 1 || values[0] == "" {
				return 0, 0, domain.ErrInvalidInput
			}
			n, err := strconv.Atoi(values[0])
			if err != nil {
				return 0, 0, domain.ErrInvalidInput
			}
			*p.target = n
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return 0, 0, domain.ErrInvalidInput
	}
	return limit, offset, nil
}
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, domain.ErrNotFound
	}
	return id, nil
}
func apiError(w http.ResponseWriter, err error, log *zap.Logger) {
	status, message := http.StatusInternalServerError, "internal error"
	switch {
	case errors.Is(err, domain.ErrSearchUnavailable):
		status, message = 503, "Поиск временно недоступен. Попробуйте ещё раз позже."
	case errors.Is(err, domain.ErrAccessDenied):
		status, message = 403, "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, message = 404, "not found"
	case errors.Is(err, domain.ErrAlreadyExists):
		status, message = 409, "already exists"
	case errors.Is(err, domain.ErrInvalidState):
		status, message = 409, "job must be failed before retry"
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, service.ErrInvalidPassword):
		status, message = 400, "invalid input"
	case errors.Is(err, service.ErrFileTooLarge):
		status, message = 413, "document too large"
	default:
		log.Error("request operation failed", zap.Error(err))
	}
	middleware.JSONError(w, status, message)
}
