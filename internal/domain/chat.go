package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrChatArchived = errors.New("chat session is archived")
	ErrChatConflict = errors.New("client message ID already used with different content")
)

type ChatSession struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"-"`
	Title      string     `json:"title"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ArchivedAt *time.Time `json:"archived_at"`
}

type ChatHistory struct {
	Session  ChatSession   `json:"session"`
	Messages []ChatMessage `json:"messages"`
}

type ChatTurn struct {
	UserMessage      ChatMessage `json:"user_message"`
	AssistantMessage ChatMessage `json:"assistant_message"`
}
