package handlers

import (
	"context"
	"net/http"

	"ai-assistant/internal/domain"
	"go.uber.org/zap"
)

type Users interface {
	Create(context.Context, domain.User, string, string) (domain.User, error)
	List(context.Context, domain.User, int, int) ([]domain.User, int64, error)
}
type UserHandler struct {
	users Users
	log   *zap.Logger
}

func NewUsers(users Users, log *zap.Logger) *UserHandler { return &UserHandler{users: users, log: log} }
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &input); err != nil {
		apiError(w, err, h.log)
		return
	}
	user, err := h.users.Create(r.Context(), actor(r), input.Username, input.Password)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 201, listedUsers([]domain.User{user})[0])
}
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	users, total, err := h.users.List(r.Context(), actor(r), limit, offset)
	if err != nil {
		apiError(w, err, h.log)
		return
	}
	writeJSON(w, 200, struct {
		Items []listedUser `json:"items"`
		Total int64        `json:"total"`
	}{listedUsers(users), total})
}
