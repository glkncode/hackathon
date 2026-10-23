package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeUsers struct {
	mu      sync.Mutex
	user    domain.User
	err     error
	inserts int
}

func (f *fakeUsers) GetByUsername(ctx context.Context, name string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.User{}, f.err
	}
	if !strings.EqualFold(name, f.user.Username) || f.user.ID == uuid.Nil {
		return domain.User{}, domain.ErrNotFound
	}
	return f.user, nil
}
func (f *fakeUsers) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.User{}, f.err
	}
	if id != f.user.ID || id == uuid.Nil {
		return domain.User{}, domain.ErrNotFound
	}
	return f.user, nil
}
func (f *fakeUsers) HasUsers(ctx context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.user.ID != uuid.Nil, f.err
}
func (f *fakeUsers) CreateIfEmpty(ctx context.Context, user domain.User) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if f.user.ID != uuid.Nil {
		return false, nil
	}
	user.ID = uuid.New()
	f.user = user
	f.inserts++
	return true, nil
}

type fakeSessions struct {
	items map[string]domain.Session
	err   error
}

func (f *fakeSessions) Create(ctx context.Context, s domain.Session) (domain.Session, error) {
	if f.err != nil {
		return domain.Session{}, f.err
	}
	s.ID = uuid.New()
	s.CreatedAt = time.Now()
	f.items[string(s.TokenHash)] = s
	return s, nil
}
func (f *fakeSessions) GetByTokenHash(ctx context.Context, hash []byte) (domain.Session, error) {
	if f.err != nil {
		return domain.Session{}, f.err
	}
	s, ok := f.items[string(hash)]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}
func (f *fakeSessions) Revoke(ctx context.Context, id uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	for key, s := range f.items {
		if s.ID == id {
			if s.RevokedAt == nil {
				now := time.Now()
				s.RevokedAt = &now
				f.items[key] = s
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func authFixture(t *testing.T) (*AuthService, *fakeUsers, *fakeSessions, *observer.ObservedLogs) {
	t.Helper()
	hash, err := HashPassword("correct-password-123")
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUsers{user: domain.User{ID: uuid.New(), Username: "admin", PasswordHash: hash, Role: domain.RoleAdmin, IsActive: true}}
	sessions := &fakeSessions{items: make(map[string]domain.Session)}
	core, logs := observer.New(zap.DebugLevel)
	auth, err := NewAuthService(users, sessions, zap.New(core), AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return auth, users, sessions, logs
}

func TestAuthLoginAuthenticateLogout(t *testing.T) {
	auth, users, sessions, logs := authFixture(t)
	now := time.Now()
	auth.now = func() time.Time { return now }
	result, err := auth.Login(context.Background(), "admin", "correct-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SessionToken) != 43 || len(result.CSRFToken) != 43 || result.SessionToken == result.CSRFToken || len(sessions.items) != 1 || result.Session.ExpiresAt.Sub(now) != 12*time.Hour {
		t.Fatal("invalid token generation, session creation or default TTL")
	}
	for token, stored := range map[string][]byte{result.SessionToken: result.Session.TokenHash, result.CSRFToken: result.Session.CSRFTokenHash} {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(raw) != 32 || bytes.Equal(stored, raw) {
			t.Fatal("raw token persisted")
		}
		hash, err := HashToken(token)
		if err != nil || !bytes.Equal(hash, stored) {
			t.Fatal("incorrect token hash")
		}
	}
	identity, err := auth.Authenticate(context.Background(), result.SessionToken)
	if err != nil || identity.User.ID != users.user.ID || identity.Session.ID != result.Session.ID {
		t.Fatal("valid session rejected")
	}
	if err := auth.Logout(context.Background(), identity.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err := auth.Logout(context.Background(), identity.Session.ID); err != nil {
		t.Fatal("repeat logout failed")
	}
	if err := auth.Logout(context.Background(), uuid.New()); err != nil {
		t.Fatal("unknown-session logout failed")
	}
	if _, err := auth.Authenticate(context.Background(), result.SessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked session authenticated")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	allLogs, _ := json.Marshal(logs.All())
	for _, secret := range []string{result.SessionToken, result.CSRFToken, users.user.PasswordHash, "correct-password-123"} {
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(allLogs, []byte(secret)) {
			t.Fatal("secret leaked to JSON or logs")
		}
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	auth, users, sessions, logs := authFixture(t)
	for _, tc := range []struct {
		username, password string
		inactive           bool
	}{
		{"admin", "wrong-password", false}, {"unknown", "correct-password-123", false}, {"admin", "correct-password-123", true},
	} {
		users.user.IsActive = !tc.inactive
		if _, err := auth.Login(context.Background(), tc.username, tc.password); err != ErrInvalidCredentials {
			t.Fatalf("unexpected credential error: %v", err)
		}
	}
	if len(sessions.items) != 0 || logs.FilterMessage("login failed").Len() != 3 {
		t.Fatal("failed login created a session or changed its log message")
	}
}

func TestAuthenticateInvalidStates(t *testing.T) {
	for _, state := range []string{"expired", "revoked", "inactive", "missing user", "missing session", "malformed token"} {
		t.Run(state, func(t *testing.T) {
			auth, users, sessions, _ := authFixture(t)
			result, err := auth.Login(context.Background(), "admin", "correct-password-123")
			if err != nil {
				t.Fatal(err)
			}
			s := result.Session
			switch state {
			case "expired":
				s.ExpiresAt = auth.now()
			case "revoked":
				now := auth.now()
				s.RevokedAt = &now
			case "inactive":
				users.user.IsActive = false
			case "missing user":
				users.user.ID = uuid.Nil
			case "missing session":
				delete(sessions.items, string(s.TokenHash))
			case "malformed token":
				result.SessionToken = "bad"
			}
			if state != "missing session" {
				sessions.items[string(s.TokenHash)] = s
			}
			if _, err := auth.Authenticate(context.Background(), result.SessionToken); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("invalid state accepted: %v", err)
			}
		})
	}
}

func TestAuthErrorsAndCancellation(t *testing.T) {
	auth, users, sessions, _ := authFixture(t)
	want := errors.New("database unavailable")
	users.err = want
	if _, err := auth.Login(context.Background(), "admin", "correct-password-123"); !errors.Is(err, want) {
		t.Fatal("database error hidden")
	}
	users.err = nil
	sessions.err = want
	if _, err := auth.Login(context.Background(), "admin", "correct-password-123"); !errors.Is(err, want) {
		t.Fatal("session creation error hidden")
	}
	sessions.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := auth.Login(ctx, "admin", "correct-password-123"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if _, err := NewAuthService(users, sessions, zap.NewNop(), AuthConfig{SessionTTL: -time.Second}); err == nil {
		t.Fatal("invalid TTL accepted")
	}
	if _, err := NewAuthService(nil, sessions, zap.NewNop(), AuthConfig{}); err == nil {
		t.Fatal("nil repository accepted")
	}
}
