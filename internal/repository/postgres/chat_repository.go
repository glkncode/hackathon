package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

type ChatRepository struct{ db *sql.DB }

func NewChatRepository(db *sql.DB) *ChatRepository { return &ChatRepository{db: db} }

const chatSessionColumns = `s.id,s.user_id,s.title,s.created_at,s.updated_at,s.archived_at`
const chatOwner = ` FROM chat_sessions s JOIN users u ON u.id=s.user_id AND u.is_active WHERE s.user_id=$1`

func scanChatSession(row scanner) (domain.ChatSession, error) {
	var s domain.ChatSession
	err := row.Scan(&s.ID, &s.UserID, &s.Title, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt)
	return s, mapError("read chat session", err)
}

func chatPage(limit, offset int) error {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return domain.ErrInvalidInput
	}
	return nil
}

func (r *ChatRepository) CreateSession(ctx context.Context, owner uuid.UUID, title string) (domain.ChatSession, error) {
	if owner == uuid.Nil || !utf8.ValidString(title) || strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 200 || strings.ContainsRune(title, 0) {
		return domain.ChatSession{}, domain.ErrInvalidInput
	}
	return scanChatSession(r.db.QueryRowContext(ctx, `INSERT INTO chat_sessions AS s (user_id,title)
		SELECT id,$2 FROM users WHERE id=$1 AND is_active RETURNING `+chatSessionColumns, owner, title))
}

func (r *ChatRepository) GetSessionByID(ctx context.Context, owner, id uuid.UUID) (domain.ChatSession, error) {
	return scanChatSession(r.db.QueryRowContext(ctx, `SELECT `+chatSessionColumns+chatOwner+` AND s.id=$2`, owner, id))
}

// Archived sessions remain readable by their owner, but are absent from the list.
func (r *ChatRepository) ListSessionsByUser(ctx context.Context, owner uuid.UUID, limit, offset int) ([]domain.ChatSession, error) {
	if err := chatPage(limit, offset); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+chatSessionColumns+chatOwner+` AND s.archived_at IS NULL ORDER BY s.updated_at DESC,s.id LIMIT $2 OFFSET $3`, owner, limit, offset)
	if err != nil {
		return nil, mapError("list chat sessions", err)
	}
	defer rows.Close()
	items := make([]domain.ChatSession, 0)
	for rows.Next() {
		item, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, mapError("list chat sessions", rows.Err())
}

func (r *ChatRepository) ArchiveSession(ctx context.Context, owner, id uuid.UUID) error {
	var found uuid.UUID
	// UPDATE takes the same row lock as saving a turn. Repeated archive is safe.
	err := r.db.QueryRowContext(ctx, `UPDATE chat_sessions s SET archived_at=COALESCE(archived_at,clock_timestamp()),
		updated_at=CASE WHEN archived_at IS NULL THEN clock_timestamp() ELSE updated_at END
		WHERE s.user_id=$1 AND s.id=$2 AND EXISTS (SELECT 1 FROM users WHERE id=$1 AND is_active) RETURNING id`, owner, id).Scan(&found)
	return mapError("archive chat session", err)
}

type chatQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// All paths, including idempotent replay and recent context, use this ACL-aware
// projection. Sources are never cached or copied into assistant content.
func readChatMessages(ctx context.Context, q chatQuerier, owner, session uuid.UUID, condition string, args ...any) ([]domain.ChatMessage, error) {
	if db, ok := q.(*sql.DB); ok {
		// Content redaction, provenance and cards use one consistent ACL snapshot.
		// This short read transaction ends before any search or model HTTP call.
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return nil, mapError("begin chat history", err)
		}
		defer tx.Rollback()
		items, err := readChatMessageRows(ctx, tx, owner, session, condition, args...)
		if err != nil {
			return nil, err
		}
		return items, mapError("commit chat history", tx.Commit())
	}
	return readChatMessageRows(ctx, q, owner, session, condition, args...)
}

func readChatMessageRows(ctx context.Context, q chatQuerier, owner, session uuid.UUID, condition string, args ...any) ([]domain.ChatMessage, error) {
	params := append([]any{owner, session}, args...)
	rows, err := q.QueryContext(ctx, `SELECT m.id,m.session_id,m.role,
		CASE WHEN guard.hidden THEN '' ELSE m.content END,
		m.mode,m.client_message_id,m.reply_to_id,m.created_at,guard.hidden,
		CASE WHEN guard.hidden THEN '[]'::json ELSE COALESCE(deps.ids,'[]'::json) END
		FROM chat_messages m JOIN chat_sessions s ON s.id=m.session_id JOIN users u ON u.id=s.user_id AND u.is_active
		LEFT JOIN LATERAL (
			SELECT count(*) AS total,
			count(*) FILTER (WHERE u.role='admin' OR EXISTS (SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id)) AS visible,
			json_agg(dep.chunk_id) AS ids
			FROM chat_message_dependencies dep JOIN document_chunks c ON c.id=dep.chunk_id
			JOIN document_versions v ON v.id=c.document_version_id JOIN documents d ON d.id=v.document_id
			WHERE dep.message_id=m.id
		) deps ON true
		CROSS JOIN LATERAL (SELECT COALESCE(m.mode IN ('generated','clarification') AND (deps.total=0 OR deps.total<>deps.visible),false) AS hidden) guard
		WHERE s.user_id=$1 AND s.id=$2 `+condition, params...)
	if err != nil {
		return nil, mapError("list chat messages", err)
	}
	items := make([]domain.ChatMessage, 0)
	ids := make([]string, 0)
	for rows.Next() {
		var m domain.ChatMessage
		var dependencies []byte
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Mode, &m.ClientMessageID, &m.ReplyToID, &m.CreatedAt, &m.ContentUnavailable, &dependencies); err != nil {
			rows.Close()
			return nil, mapError("read chat message", err)
		}
		if err := json.Unmarshal(dependencies, &m.DependencyIDs); err != nil {
			rows.Close()
			return nil, mapError("read chat provenance", err)
		}
		if m.ContentUnavailable {
			m.Content = domain.ChatContentUnavailable
		}
		m.Sources = make([]domain.ChatMessageSource, 0)
		items = append(items, m)
		ids = append(ids, m.ID.String())
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, mapError("read chat messages", err)
	}
	if len(items) == 0 {
		return items, nil
	}
	rows, err = q.QueryContext(ctx, `SELECT ms.message_id,ms.position,c.id,d.id,v.id,d.title,v.version_number,c.chunk_index,
		c.content,c.page_number,metadata.section_number,metadata.section_title,ms.score
		FROM chat_message_sources ms JOIN chat_messages m ON m.id=ms.message_id
		JOIN chat_sessions s ON s.id=m.session_id JOIN users u ON u.id=s.user_id AND u.is_active
		JOIN document_chunks c ON c.id=ms.chunk_id JOIN document_versions v ON v.id=c.document_version_id
		JOIN documents d ON d.id=v.document_id
		`+chunkMetadataSQL+`
		WHERE s.user_id=$1 AND s.id=$2 AND m.id=ANY($3::uuid[])
		AND (u.role='admin' OR EXISTS (SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))
		ORDER BY ms.message_id,ms.position`, owner, session, ids)
	if err != nil {
		return nil, mapError("hydrate chat sources", err)
	}
	defer rows.Close()
	indexes := make(map[uuid.UUID]int, len(items))
	for i := range items {
		indexes[items[i].ID] = i
	}
	for rows.Next() {
		var id uuid.UUID
		var s domain.ChatMessageSource
		if err := rows.Scan(&id, &s.Position, &s.ChunkID, &s.DocumentID, &s.DocumentVersionID, &s.DocumentTitle, &s.VersionNumber, &s.ChunkIndex, &s.Content, &s.PageNumber, &s.SectionNumber, &s.SectionTitle, &s.Score); err != nil {
			return nil, mapError("read chat source", err)
		}
		i := indexes[id]
		items[i].Sources = append(items[i].Sources, s)
	}
	for i := range items {
		if items[i].ContentUnavailable {
			items[i].Sources = make([]domain.ChatMessageSource, 0)
		}
	}
	return items, mapError("hydrate chat sources", rows.Err())
}

func (r *ChatRepository) ListMessages(ctx context.Context, owner, session uuid.UUID, limit, offset int) ([]domain.ChatMessage, error) {
	if err := chatPage(limit, offset); err != nil {
		return nil, err
	}
	if _, err := r.GetSessionByID(ctx, owner, session); err != nil {
		return nil, err
	}
	return readChatMessages(ctx, r.db, owner, session, `ORDER BY m.position LIMIT $3 OFFSET $4`, limit, offset)
}

// RecentMessages returns at most 100 messages, in chronological order. The same
// current ACL applies to generation context as to the public history endpoint.
func (r *ChatRepository) RecentMessages(ctx context.Context, owner, session uuid.UUID, limit int) ([]domain.ChatMessage, error) {
	if err := chatPage(limit, 0); err != nil {
		return nil, err
	}
	if _, err := r.GetSessionByID(ctx, owner, session); err != nil {
		return nil, err
	}
	items, err := readChatMessages(ctx, r.db, owner, session, `ORDER BY m.position DESC LIMIT $3`, limit)
	slices.Reverse(items)
	return items, err
}

func readChatTurn(ctx context.Context, q chatQuerier, owner, session, key uuid.UUID) (domain.ChatTurn, error) {
	items, err := readChatMessages(ctx, q, owner, session, `AND (m.client_message_id=$3 OR m.reply_to_id=(SELECT id FROM chat_messages WHERE session_id=$2 AND client_message_id=$3)) ORDER BY m.position`, key)
	if err != nil {
		return domain.ChatTurn{}, err
	}
	if len(items) == 0 {
		return domain.ChatTurn{}, domain.ErrNotFound
	}
	if len(items) != 2 || items[0].Role != domain.ChatRoleUser || items[1].Role != domain.ChatRoleAssistant {
		return domain.ChatTurn{}, domain.ErrInvalidState
	}
	return domain.ChatTurn{UserMessage: items[0], AssistantMessage: items[1]}, nil
}

func (r *ChatRepository) GetTurnByClientID(ctx context.Context, owner, session, key uuid.UUID) (domain.ChatTurn, error) {
	if _, err := r.GetSessionByID(ctx, owner, session); err != nil {
		return domain.ChatTurn{}, err
	}
	return readChatTurn(ctx, r.db, owner, session, key)
}

// SaveMessageWithSources saves a complete turn. The session row serializes sends,
// archives and duplicate keys across processes; search happens before BeginTx.
// Only source IDs, order and PostgreSQL relevance are persisted, never content.
func (r *ChatRepository) SaveMessageWithSources(ctx context.Context, owner, session, key uuid.UUID, turn domain.ChatTurn, sources []domain.SearchResult) (domain.ChatTurn, error) {
	query, err := (domain.SearchQuery{Query: turn.UserMessage.Content}).Normalize()
	if err != nil || key == uuid.Nil || len(sources) > domain.MaxSearchLimit || turn.UserMessage.Role != domain.ChatRoleUser || turn.AssistantMessage.Role != domain.ChatRoleAssistant || turn.AssistantMessage.Mode == nil {
		return domain.ChatTurn{}, domain.ErrInvalidInput
	}
	mode, text := *turn.AssistantMessage.Mode, turn.AssistantMessage.Content
	generated := mode == domain.ChatModeGenerated || mode == domain.ChatModeClarification
	if generated {
		if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || len(text) > domain.MaxChatAssistantBytes || utf8.RuneCountInString(text) > domain.MaxChatAssistantRunes || len(turn.AssistantMessage.DependencyIDs) == 0 || len(turn.AssistantMessage.DependencyIDs) > domain.MaxChatDependencies || (mode == domain.ChatModeGenerated && len(sources) == 0) {
			return domain.ChatTurn{}, domain.ErrInvalidInput
		}
	} else {
		expectedMode, expectedText := domain.SearchReply(len(sources) > 0)
		if mode != expectedMode || text != expectedText || len(turn.AssistantMessage.DependencyIDs) != 0 {
			return domain.ChatTurn{}, domain.ErrInvalidInput
		}
	}
	dependencies := make(map[uuid.UUID]bool, len(turn.AssistantMessage.DependencyIDs))
	for _, id := range turn.AssistantMessage.DependencyIDs {
		if id == uuid.Nil || dependencies[id] {
			return domain.ChatTurn{}, domain.ErrInvalidInput
		}
		dependencies[id] = true
	}
	seen := make(map[uuid.UUID]bool, len(sources))
	for _, s := range sources {
		if s.ChunkID == uuid.Nil || seen[s.ChunkID] || s.Score < 0 || math.IsNaN(s.Score) || math.IsInf(s.Score, 0) {
			return domain.ChatTurn{}, domain.ErrInvalidInput
		}
		seen[s.ChunkID] = true
		if generated && !dependencies[s.ChunkID] {
			return domain.ChatTurn{}, domain.ErrInvalidInput
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChatTurn{}, mapError("begin chat turn", err)
	}
	defer tx.Rollback()
	s, err := scanChatSession(tx.QueryRowContext(ctx, `SELECT `+chatSessionColumns+chatOwner+` AND s.id=$2 FOR UPDATE OF s`, owner, session))
	if err != nil {
		return domain.ChatTurn{}, err
	}
	if s.ArchivedAt != nil {
		return domain.ChatTurn{}, domain.ErrChatArchived
	}
	existing, err := readChatTurn(ctx, tx, owner, session, key)
	if err == nil {
		if existing.UserMessage.Content != query.Query {
			return domain.ChatTurn{}, domain.ErrChatConflict
		}
		if err := tx.Commit(); err != nil {
			return domain.ChatTurn{}, mapError("commit chat replay", err)
		}
		return r.GetTurnByClientID(ctx, owner, session, key)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.ChatTurn{}, err
	}
	var userID, assistantID uuid.UUID
	err = tx.QueryRowContext(ctx, `INSERT INTO chat_messages (session_id,role,content,client_message_id) VALUES ($1,'user',$2,$3) RETURNING id`, session, query.Query, key).Scan(&userID)
	if err != nil {
		return domain.ChatTurn{}, mapError("save chat user message", err)
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO chat_messages (session_id,role,content,mode,reply_to_id) VALUES ($1,'assistant',$2,$3,$4) RETURNING id`, session, text, mode, userID).Scan(&assistantID)
	if err != nil {
		return domain.ChatTurn{}, mapError("save chat assistant message", err)
	}
	for i, source := range sources {
		result, err := tx.ExecContext(ctx, `INSERT INTO chat_message_sources (message_id,chunk_id,position,score)
			SELECT $1,c.id,$3,$4 FROM document_chunks c JOIN document_versions v ON v.id=c.document_version_id
			JOIN documents d ON d.id=v.document_id JOIN users u ON u.id=$5 AND u.is_active
			WHERE c.id=$2 AND v.status='ready' AND (u.role='admin' OR EXISTS
			(SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))`, assistantID, source.ChunkID, i+1, source.Score, owner)
		if err != nil {
			return domain.ChatTurn{}, mapError("save chat source", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return domain.ChatTurn{}, mapError("count chat source", err)
		}
		// ACL or chunk identity changed since search. Roll back the whole pair.
		if count != 1 {
			return domain.ChatTurn{}, domain.ErrInvalidState
		}
	}
	for _, id := range turn.AssistantMessage.DependencyIDs {
		result, err := tx.ExecContext(ctx, `INSERT INTO chat_message_dependencies(message_id,chunk_id)
			SELECT $1,c.id FROM document_chunks c JOIN document_versions v ON v.id=c.document_version_id
			JOIN documents d ON d.id=v.document_id JOIN users u ON u.id=$3 AND u.is_active
			WHERE c.id=$2 AND (u.role='admin' OR EXISTS (SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id))`, assistantID, id, owner)
		if err != nil {
			return domain.ChatTurn{}, mapError("save chat provenance", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return domain.ChatTurn{}, mapError("count chat provenance", err)
		}
		if count != 1 {
			return domain.ChatTurn{}, domain.ErrInvalidState
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=clock_timestamp() WHERE user_id=$1 AND id=$2`, owner, session); err != nil {
		return domain.ChatTurn{}, mapError("touch chat session", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.ChatTurn{}, mapError("commit chat turn", err)
	}
	// Rehydrate after commit so response and replay both use the current ACL.
	return r.GetTurnByClientID(ctx, owner, session, key)
}
