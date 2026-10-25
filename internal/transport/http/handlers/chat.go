package handlers

import (
	"errors"
	"net/http"
	"time"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"ai-assistant/internal/transport/http/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatHandler struct {
	service *service.ChatService
	log     *zap.Logger
}

func NewChat(chat *service.ChatService, log *zap.Logger) *ChatHandler {
	return &ChatHandler{service: chat, log: log}
}

func (h *ChatHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrFileTooLarge), errors.Is(err, domain.ErrTemplateLimit):
		middleware.JSONError(w, 413, "document input too large")
	case errors.Is(err, domain.ErrRevisionConflict):
		middleware.JSONError(w, 409, "Черновик изменён в другом окне. Обновите данные и повторите действие.")
	case service.TemplateEngineFailure(err), errors.Is(err, domain.ErrTemplateBusy):
		middleware.JSONError(w, 503, "Подготовка документа временно недоступна. Черновик сохранён; повторите действие позже.")
	case errors.Is(err, domain.ErrSearchUnavailable):
		middleware.JSONError(w, 503, "Поиск временно недоступен. Попробуйте отправить вопрос ещё раз позже.")
	case errors.Is(err, domain.ErrChatArchived):
		middleware.JSONError(w, 409, "chat session is archived")
	case errors.Is(err, domain.ErrChatConflict):
		middleware.JSONError(w, 409, "client_message_id already used with different content")
	case errors.Is(err, domain.ErrInvalidState):
		middleware.JSONError(w, 409, "sources changed; retry the message")
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrAccessDenied), errors.Is(err, domain.ErrAlreadyExists):
		apiError(w, err, h.log)
	default:
		h.log.Error("chat request failed")
		middleware.JSONError(w, 500, "internal error")
	}
}

func (h *ChatHandler) Document(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	view, err := h.service.DocumentView(r.Context(), actor(r), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, view)
}
func (h *ChatHandler) DocumentAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var input service.ChatDocumentAction
	if err = readTemplateJSON(w, r, &input); err != nil {
		h.fail(w, err)
		return
	}
	turn, err := h.service.SendDocumentAction(r.Context(), actor(r), id, input)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, turn)
}

func (h *ChatHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input *struct {
		Title string `json:"title"`
	}
	if err := readJSON(w, r, &input); err != nil {
		h.fail(w, err)
		return
	}
	if input == nil {
		h.fail(w, domain.ErrInvalidInput)
		return
	}
	result, err := h.service.CreateSession(r.Context(), actor(r), input.Title)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 201, result)
}

func (h *ChatHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	items, err := h.service.ListSessions(r.Context(), actor(r), limit, offset)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, struct {
		Sessions []domain.ChatSession `json:"sessions"`
		Limit    int                  `json:"limit"`
		Offset   int                  `json:"offset"`
	}{items, limit, offset})
}

func (h *ChatHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	limit, offset, err := pagination(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	history, err := h.service.GetSession(r.Context(), actor(r), id, limit, offset)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, struct {
		domain.ChatHistory
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}{history, limit, offset})
}

func (h *ChatHandler) Send(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var input struct {
		Content         string    `json:"content"`
		ClientMessageID uuid.UUID `json:"client_message_id"`
	}
	if err := readJSON(w, r, &input); err != nil {
		h.fail(w, err)
		return
	}
	turn, err := h.service.SendMessage(r.Context(), actor(r), id, input.ClientMessageID, input.Content)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, struct {
		UserMessageID      uuid.UUID                  `json:"user_message_id"`
		AssistantMessageID uuid.UUID                  `json:"assistant_message_id"`
		Mode               *domain.ChatMode           `json:"mode"`
		Message            string                     `json:"message"`
		Sources            []domain.ChatMessageSource `json:"sources"`
		UserCreatedAt      time.Time                  `json:"user_created_at"`
		AssistantCreatedAt time.Time                  `json:"assistant_created_at"`
	}{turn.UserMessage.ID, turn.AssistantMessage.ID, turn.AssistantMessage.Mode, turn.AssistantMessage.Content, turn.AssistantMessage.Sources, turn.UserMessage.CreatedAt, turn.AssistantMessage.CreatedAt})
}

func (h *ChatHandler) Archive(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.service.ArchiveSession(r.Context(), actor(r), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
