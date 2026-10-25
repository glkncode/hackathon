package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatStore interface {
	CreateSession(context.Context, uuid.UUID, string) (domain.ChatSession, error)
	GetSessionByID(context.Context, uuid.UUID, uuid.UUID) (domain.ChatSession, error)
	ListSessionsByUser(context.Context, uuid.UUID, int, int) ([]domain.ChatSession, error)
	ArchiveSession(context.Context, uuid.UUID, uuid.UUID) error
	ListMessages(context.Context, uuid.UUID, uuid.UUID, int, int) ([]domain.ChatMessage, error)
	GetTurnByClientID(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.ChatTurn, error)
	SaveMessageWithSources(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, domain.ChatTurn, []domain.SearchResult) (domain.ChatTurn, error)
	RecentMessages(context.Context, uuid.UUID, uuid.UUID, int) ([]domain.ChatMessage, error)
}

type ChatSearch interface {
	Search(context.Context, domain.User, domain.SearchQuery) ([]domain.SearchResult, error)
}

type ChatService struct {
	repository     ChatStore
	search         ChatSearch
	log            *zap.Logger
	rag            *chatRAG
	contextMetrics resolverMetrics
	documents      *chatDocuments
}

func NewChatService(repository ChatStore, search ChatSearch, log *zap.Logger) *ChatService {
	return &ChatService{repository: repository, search: search, log: log}
}

func chatActor(actor domain.User) error {
	if actor.ID == uuid.Nil || !actor.IsActive || !actor.Role.Valid() {
		return domain.ErrAccessDenied
	}
	return nil
}

func chatPagination(limit, offset int) error {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return domain.ErrInvalidInput
	}
	return nil
}

func (s *ChatService) CreateSession(ctx context.Context, actor domain.User, title string) (domain.ChatSession, error) {
	if err := chatActor(actor); err != nil {
		return domain.ChatSession{}, err
	}
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 200 || strings.ContainsRune(title, 0) {
		return domain.ChatSession{}, domain.ErrInvalidInput
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Новый диалог"
	}
	session, err := s.repository.CreateSession(ctx, actor.ID, title)
	if err == nil {
		s.log.Info("chat session created", zap.String("session_id", session.ID.String()), zap.String("user_id", actor.ID.String()))
	}
	return session, err
}

func (s *ChatService) ListSessions(ctx context.Context, actor domain.User, limit, offset int) ([]domain.ChatSession, error) {
	if err := chatActor(actor); err != nil {
		return nil, err
	}
	if err := chatPagination(limit, offset); err != nil {
		return nil, err
	}
	return s.repository.ListSessionsByUser(ctx, actor.ID, limit, offset)
}

func (s *ChatService) GetSession(ctx context.Context, actor domain.User, id uuid.UUID, limit, offset int) (domain.ChatHistory, error) {
	if err := chatActor(actor); err != nil {
		return domain.ChatHistory{}, err
	}
	if err := chatPagination(limit, offset); err != nil {
		return domain.ChatHistory{}, err
	}
	session, err := s.repository.GetSessionByID(ctx, actor.ID, id)
	if err != nil {
		return domain.ChatHistory{}, err
	}
	messages, err := s.repository.ListMessages(ctx, actor.ID, id, limit, offset)
	return domain.ChatHistory{Session: session, Messages: messages}, err
}

func (s *ChatService) ArchiveSession(ctx context.Context, actor domain.User, id uuid.UUID) error {
	if err := chatActor(actor); err != nil {
		return err
	}
	err := s.repository.ArchiveSession(ctx, actor.ID, id)
	if err == nil {
		s.log.Info("chat session archived", zap.String("session_id", id.String()), zap.String("user_id", actor.ID.String()))
	}
	return err
}

// RecentMessages is a bounded, ownership- and ACL-filtered context boundary for
// generation and the optional private context resolver.
func (s *ChatService) RecentMessages(ctx context.Context, actor domain.User, id uuid.UUID, limit int) ([]domain.ChatMessage, error) {
	if err := chatActor(actor); err != nil {
		return nil, err
	}
	if err := chatPagination(limit, 0); err != nil {
		return nil, err
	}
	return s.repository.RecentMessages(ctx, actor.ID, id, limit)
}

func (s *ChatService) SendMessage(ctx context.Context, actor domain.User, id, key uuid.UUID, content string) (turn domain.ChatTurn, err error) {
	defer func() {
		if err != nil {
			// Dependency error strings may contain a query or remote response. Do
			// not put them into logs; IDs and stage-independent failure suffice.
			s.log.Warn("chat message processing failed", zap.String("session_id", id.String()), zap.String("user_id", actor.ID.String()))
		}
	}()
	if err = chatActor(actor); err != nil {
		return turn, err
	}
	session, err := s.repository.GetSessionByID(ctx, actor.ID, id)
	if err != nil {
		return turn, err
	}
	if session.ArchivedAt != nil {
		return turn, domain.ErrChatArchived
	}
	query, err := (domain.SearchQuery{Query: content}).Normalize()
	if err != nil {
		return turn, err
	}
	if key == uuid.Nil {
		return turn, domain.ErrInvalidInput
	}
	previous, err := s.repository.GetTurnByClientID(ctx, actor.ID, id, key)
	if err == nil {
		if previous.UserMessage.Content != query.Query {
			return turn, domain.ErrChatConflict
		}
		return previous, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return turn, err
	}
	if s.documents != nil {
		docTurn, handled, docErr := s.routeDocumentMessage(ctx, actor, id, key, query.Query)
		if handled {
			return docTurn, docErr
		}
	}
	var history []domain.ChatMessage
	var contextDeps []uuid.UUID
	effectiveQuery := query.Query
	var planned []string
	contextResolved := false
	startsNewTopic := false
	var pendingClarification *domain.ChatMessage
	if s.rag != nil && s.rag.config.ContextResolverEnabled {
		if s.rag.config.MaxHistoryMessages > 0 {
			history, err = s.repository.RecentMessages(ctx, actor.ID, id, s.rag.config.MaxHistoryMessages)
			if err != nil {
				return turn, err
			}
			history = activeTopicHistory(history)
		}
		result, queries, deps, resolveErr := s.resolveContext(ctx, id, query.Query, history)
		if ctx.Err() != nil {
			return turn, ctx.Err()
		}
		if resolveErr == nil {
			if result.Status == "needs_clarification" {
				mode := domain.ChatModeClarification
				pendingClarification = &domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: *result.ClarificationQuestion, Mode: &mode, DependencyIDs: deps}
				contextDeps = deps
			} else {
				effectiveQuery = queries[0]
				planned = queries
				contextDeps = deps
				contextResolved = true
				startsNewTopic = result.Status == "independent"
				if startsNewTopic {
					history = nil
				}
			}
		}
	}
	start := time.Now()
	var sources []domain.SearchResult
	var verdict domain.RetrievalDisposition
	if s.rag != nil && s.rag.config.ContextResolverEnabled {
		var branches []string
		if contextResolved {
			branches = append([]string{query.Query}, planned...)
		}
		var retrieval domain.RetrievalResult
		retrieval, err = s.search.(ContextSearch).SearchForChat(ctx, actor, domain.SearchQuery{Query: effectiveQuery, Limit: query.Limit}, branches)
		if err == nil && retrieval.Validate() != nil {
			err = domain.ErrSearchUnavailable
		}
		sources, verdict = retrieval.Sources, retrieval.Disposition
	} else {
		sources, err = s.search.Search(ctx, actor, query)
	}
	s.log.Info("chat search completed", zap.String("session_id", id.String()), zap.Duration("duration", time.Since(start)), zap.Int("source_count", len(sources)), zap.Bool("success", err == nil))
	if err != nil {
		return turn, err
	}
	mode, message := domain.SearchReply(len(sources) > 0)
	turn = domain.ChatTurn{
		UserMessage:      domain.ChatMessage{Role: domain.ChatRoleUser, Content: query.Query, StartsNewTopic: startsNewTopic},
		AssistantMessage: domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: message, Mode: &mode},
	}
	// Clarification is provisional: authorized evidence plus the session history
	// can establish the intended subject more accurately than a query-only plan.
	// Still run the unchanged original branch and Evidence Gate before asking.
	if pendingClarification != nil && len(sources) == 0 && verdict == domain.RetrievalRejected {
		turn.AssistantMessage = *pendingClarification
		s.log.Info("chat clarification retained", zap.String("session_id", id.String()), zap.String("reason", "unresolved_after_search"))
	}
	if s.rag != nil && len(sources) > 0 {
		// Read current, ACL-filtered history after retrieval, immediately before
		// building model input. No SQL transaction survives this method call.
		if !contextResolved && s.rag.config.MaxHistoryMessages > 0 {
			history, err = s.repository.RecentMessages(ctx, actor.ID, id, s.rag.config.MaxHistoryMessages)
			if err != nil {
				return turn, err
			}
			if pendingClarification != nil {
				history = activeTopicHistory(history)
			}
		}
		turn.AssistantMessage, sources = s.generateReply(ctx, id, query.Query, history, sources, turn.AssistantMessage)
		if turn.AssistantMessage.Mode != nil && (*turn.AssistantMessage.Mode == domain.ChatModeGenerated || *turn.AssistantMessage.Mode == domain.ChatModeClarification) {
			seen := map[uuid.UUID]bool{}
			for _, dep := range turn.AssistantMessage.DependencyIDs {
				seen[dep] = true
			}
			for _, dep := range contextDeps {
				if !seen[dep] {
					turn.AssistantMessage.DependencyIDs = append(turn.AssistantMessage.DependencyIDs, dep)
					seen[dep] = true
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return domain.ChatTurn{}, err
		}
	}
	return s.repository.SaveMessageWithSources(ctx, actor.ID, id, key, turn, sources)
}
