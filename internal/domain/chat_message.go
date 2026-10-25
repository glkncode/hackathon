package domain

import (
	"time"

	"github.com/google/uuid"
)

type ChatRole string
type ChatMode string

const (
	ChatRoleUser           ChatRole = "user"
	ChatRoleAssistant      ChatRole = "assistant"
	ChatModeDocument       ChatMode = "document"
	ChatModeSearch         ChatMode = "search"
	ChatModeNoSources      ChatMode = "no_sources"
	ChatModeGenerated      ChatMode = "generated"
	ChatModeClarification  ChatMode = "clarification"
	MaxChatAssistantRunes           = 8000
	MaxChatAssistantBytes           = 32 << 10
	MaxChatDependencies             = 256
	ChatContentUnavailable          = "Содержимое ответа недоступно из-за изменения прав доступа к документам."
)

// SearchReply contains no document text: revoking ACL also hides past citations.
func SearchReply(hasSources bool) (ChatMode, string) {
	if hasSources {
		return ChatModeSearch, "Найдены подходящие положения инструкции."
	}
	return ChatModeNoSources, "В доступных мне документах не нашлось подтверждённой информации по вашему вопросу. Если это поможет, укажите название документа или обстоятельства ситуации."
}

type ChatMessage struct {
	DocumentGeneratedID *uuid.UUID          `json:"document_generated_id,omitempty"`
	DocumentActionHash  string              `json:"-"`
	DocumentDraftID     *uuid.UUID          `json:"-"`
	ID                  uuid.UUID           `json:"id"`
	SessionID           uuid.UUID           `json:"session_id"`
	Role                ChatRole            `json:"role"`
	Content             string              `json:"content"`
	Mode                *ChatMode           `json:"mode"`
	ClientMessageID     *uuid.UUID          `json:"client_message_id,omitempty"`
	ReplyToID           *uuid.UUID          `json:"reply_to_id,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	Sources             []ChatMessageSource `json:"sources"`
	// Internal provenance includes every input source and transitive history
	// dependency, not merely citations the model chose to acknowledge.
	DependencyIDs      []uuid.UUID `json:"-"`
	ContentUnavailable bool        `json:"-"`
	StartsNewTopic     bool        `json:"-"`
}
