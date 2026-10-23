package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type httpChatStore struct {
	service.ChatStore
	session domain.ChatSession
	turn    domain.ChatTurn
	key     uuid.UUID
}

func (s *httpChatStore) CreateSession(_ context.Context, owner uuid.UUID, title string) (domain.ChatSession, error) {
	s.session = domain.ChatSession{ID: uuid.New(), UserID: owner, Title: title, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	return s.session, nil
}
func (s *httpChatStore) GetSessionByID(_ context.Context, owner, id uuid.UUID) (domain.ChatSession, error) {
	if s.session.UserID != owner || s.session.ID != id {
		return domain.ChatSession{}, domain.ErrNotFound
	}
	return s.session, nil
}
func (s *httpChatStore) ListSessionsByUser(_ context.Context, owner uuid.UUID, _, _ int) ([]domain.ChatSession, error) {
	if s.session.UserID == owner {
		return []domain.ChatSession{s.session}, nil
	}
	return []domain.ChatSession{}, nil
}
func (s *httpChatStore) GetTurnByClientID(_ context.Context, _, _, key uuid.UUID) (domain.ChatTurn, error) {
	if key == s.key {
		return s.turn, nil
	}
	return domain.ChatTurn{}, domain.ErrNotFound
}
func (s *httpChatStore) SaveMessageWithSources(_ context.Context, _, session, key uuid.UUID, turn domain.ChatTurn, sources []domain.SearchResult) (domain.ChatTurn, error) {
	turn.UserMessage.ID = uuid.New()
	turn.AssistantMessage.ID = uuid.New()
	turn.UserMessage.SessionID = session
	turn.AssistantMessage.SessionID = session
	turn.UserMessage.CreatedAt = time.Now()
	turn.AssistantMessage.CreatedAt = time.Now()
	turn.AssistantMessage.Sources = []domain.ChatMessageSource{}
	for i, source := range sources {
		turn.AssistantMessage.Sources = append(turn.AssistantMessage.Sources, domain.ChatMessageSource{SearchResult: source, Position: i + 1})
	}
	s.turn = turn
	s.key = key
	return turn, nil
}
func (s *httpChatStore) ListMessages(_ context.Context, _, _ uuid.UUID, _, _ int) ([]domain.ChatMessage, error) {
	if s.turn.UserMessage.ID == uuid.Nil {
		return []domain.ChatMessage{}, nil
	}
	return []domain.ChatMessage{s.turn.UserMessage, s.turn.AssistantMessage}, nil
}
func (s *httpChatStore) ArchiveSession(ctx context.Context, owner, id uuid.UUID) error {
	if _, err := s.GetSessionByID(ctx, owner, id); err != nil {
		return err
	}
	now := time.Now()
	s.session.ArchivedAt = &now
	return nil
}

func TestChatRoutes(t *testing.T) {
	_, auth := routerFixture(t)
	auth.result.User.IsActive = true
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)
	docs, users := &moduleDocuments{}, &moduleUsers{}
	access := service.NewDocumentAccessService(&moduleACL{}, docs, users, log)
	storage, err := service.NewLocalDocumentStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	store := &httpChatStore{}
	search := &httpSearchStore{}
	router, err := NewRouter(auth, log, RouterConfig{AllowedOrigins: []string{"http://localhost:8080"}}, Modules{Users: service.NewUserService(users), Documents: service.NewDocumentService(docs, access, storage, log), Access: access, MaxUploadBytes: 1024, Chat: service.NewChatService(store, service.NewSearchService(search), log)})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, csrf, origin string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "ai_session", Value: auth.result.SessionToken})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	const root = "/api/v1/chat/sessions"
	const origin = "http://localhost:8080"
	for _, route := range [][2]string{{"POST", root}, {"GET", root}, {"GET", root + "/" + uuid.NewString()}, {"POST", root + "/" + uuid.NewString() + "/messages"}, {"DELETE", root + "/" + uuid.NewString()}} {
		if w := request(route[0], route[1], `{}`, "", origin, false); w.Code != 401 {
			t.Fatal("anonymous", route, w.Code)
		}
		if route[0] != "GET" {
			if w := request(route[0], route[1], `{}`, "bad", origin, true); w.Code != 403 {
				t.Fatal("CSRF", w.Code)
			}
		}
	}
	if w := request("POST", root, `{}`, auth.result.CSRFToken, "https://evil.test", true); w.Code != 403 {
		t.Fatal("origin", w.Code)
	}
	valid := func(method, path, body string) *httptest.ResponseRecorder {
		return request(method, path, body, auth.result.CSRFToken, origin, true)
	}
	for _, body := range []string{``, `{`, `null`, `[]`, `{"user_id":"` + uuid.NewString() + `"}`, `{"title":5}`, `{} {}`, `{"title":"` + strings.Repeat("я", 201) + `"}`} {
		if w := valid("POST", root, body); w.Code != 400 {
			t.Fatal("invalid create accepted", w.Code, w.Body.String())
		}
	}
	w := valid("POST", root, `{"title":"Рабочий диалог"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := root + "/" + store.session.ID.String()
	for _, body := range []string{`{`, `null`, `{"content":"x"}`, `{"content":" ","client_message_id":"` + uuid.NewString() + `"}`, `{"content":"x","client_message_id":"invalid"}`, `{"content":"x","client_message_id":"` + uuid.NewString() + `","role":"assistant"}`} {
		if w := valid("POST", path+"/messages", body); w.Code != 400 {
			t.Fatal("invalid send accepted", w.Code, w.Body.String())
		}
	}
	key := uuid.NewString()
	body := `{"content":"секретный вопрос","client_message_id":"` + key + `"}`
	w = valid("POST", path+"/messages", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"no_sources"`) || !strings.Contains(w.Body.String(), `"sources":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		UserMessageID      uuid.UUID `json:"user_message_id"`
		AssistantMessageID uuid.UUID `json:"assistant_message_id"`
		UserCreatedAt      time.Time `json:"user_created_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.UserMessageID == uuid.Nil || response.AssistantMessageID == uuid.Nil || response.UserCreatedAt.IsZero() {
		t.Fatal("response contract", err)
	}
	if w := valid("POST", path+"/messages", body); w.Code != 200 || search.calls != 1 {
		t.Fatal("replay repeated search", w.Code)
	}
	if w := valid("POST", path+"/messages", strings.Replace(body, "секретный вопрос", "другой вопрос", 1)); w.Code != 409 {
		t.Fatal("key conflict", w.Code)
	}
	search.results = []domain.SearchResult{{ChunkID: uuid.New(), DocumentID: uuid.New(), DocumentVersionID: uuid.New(), Content: "секретный документ", DocumentTitle: "Положение", VersionNumber: 2}}
	w = valid("POST", path+"/messages", strings.Replace(body, key, uuid.NewString(), 1))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"search"`) || !strings.Contains(w.Body.String(), "секретный документ") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, route := range []string{root, path} {
		if w := valid("GET", route, ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, route := range []string{root + "?limit=101", path + "?offset=-1"} {
		if w := valid("GET", route, ""); w.Code != 400 {
			t.Fatal("pagination", w.Code)
		}
	}
	owner := store.session.UserID
	store.session.UserID = uuid.New() // Authenticated admin still cannot access this session.
	for _, route := range [][2]string{{"GET", path}, {"POST", path + "/messages"}, {"DELETE", path}} {
		if w := valid(route[0], route[1], body); w.Code != 404 {
			t.Fatal("foreign session", w.Code)
		}
	}
	store.session.UserID = owner
	search.err = errors.New("секретный вопрос: private dependency error")
	w = valid("POST", path+"/messages", strings.Replace(body, key, uuid.NewString(), 1))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := valid("DELETE", path, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := valid("POST", path+"/messages", body); w.Code != 409 || !strings.Contains(w.Body.String(), "archived") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, entry := range logs.All() {
		data, _ := json.Marshal(entry.ContextMap())
		if strings.Contains(entry.Message+string(data), "секретный") {
			t.Fatal("chat logged sensitive text")
		}
	}
}
