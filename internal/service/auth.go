package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
)

type AuthUsers interface {
	GetByUsername(context.Context, string) (domain.User, error)
	GetByID(context.Context, uuid.UUID) (domain.User, error)
}

type AuthSessions interface {
	Create(context.Context, domain.Session) (domain.Session, error)
	GetByTokenHash(context.Context, []byte) (domain.Session, error)
	Revoke(context.Context, uuid.UUID) error
}

type AuthConfig struct{ SessionTTL time.Duration }

type Identity struct {
	User    domain.User
	Session domain.Session
}

// LoginResult contains secrets for the transport only, never for JSON or logs.
type LoginResult struct {
	User         domain.User    `json:"user"`
	Session      domain.Session `json:"-"`
	SessionToken string         `json:"-"`
	CSRFToken    string         `json:"-"`
}

type AuthService struct {
	users        AuthUsers
	sessions     AuthSessions
	log          *zap.Logger
	ttl          time.Duration
	dummyHash    string
	passwordJobs chan struct{}
	now          func() time.Time
}

func NewAuthService(users AuthUsers, sessions AuthSessions, log *zap.Logger, cfg AuthConfig) (*AuthService, error) {
	if users == nil || sessions == nil || log == nil {
		return nil, errors.New("auth: repositories and logger are required")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.SessionTTL < time.Second {
		return nil, errors.New("auth: session TTL must be at least one second")
	}
	// A missing username still performs a real KDF with the same parameters.
	dummy, err := HashPassword("dummy-account-password")
	if err != nil {
		return nil, err
	}
	return &AuthService{users: users, sessions: sessions, log: log, ttl: cfg.SessionTTL,
		dummyHash: dummy, passwordJobs: make(chan struct{}, 2), now: time.Now}, nil
}

func (s *AuthService) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if len(password) > MaxPasswordBytes {
		return s.deniedLogin()
	}
	user, err := s.users.GetByUsername(ctx, username)
	found := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return LoginResult{}, s.failure("find login user", err)
	}
	encoded := s.dummyHash
	if found {
		encoded = user.PasswordHash
	}
	select {
	case s.passwordJobs <- struct{}{}:
	case <-ctx.Done():
		return LoginResult{}, ctx.Err()
	}
	valid, verifyErr := VerifyPassword(password, encoded)
	<-s.passwordJobs
	if verifyErr != nil && !errors.Is(verifyErr, ErrInvalidPassword) {
		// Do not log the damaged encoded hash or a distinguishing login response.
		s.log.Error("password verification failed")
	}
	if verifyErr != nil || !valid || !found || !user.IsActive {
		return s.deniedLogin()
	}
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}
	token, hash, err := newToken()
	if err != nil {
		return LoginResult{}, s.failure("generate session token", err)
	}
	csrf, csrfHash, err := newToken()
	if err != nil {
		return LoginResult{}, s.failure("generate CSRF token", err)
	}
	session, err := s.sessions.Create(ctx, domain.Session{
		UserID: user.ID, TokenHash: hash, CSRFTokenHash: csrfHash, ExpiresAt: s.now().Add(s.ttl),
	})
	if err != nil {
		return LoginResult{}, s.failure("create session", err)
	}
	s.log.Info("user authenticated", zap.String("user_id", user.ID.String()))
	user.PasswordHash = "" // The transport and request context never need it.
	return LoginResult{User: user, Session: session, SessionToken: token, CSRFToken: csrf}, nil
}

func (s *AuthService) deniedLogin() (LoginResult, error) {
	s.log.Warn("login failed")
	return LoginResult{}, ErrInvalidCredentials
}

func (s *AuthService) failure(operation string, err error) error {
	s.log.Error("authentication operation failed", zap.String("operation", operation), zap.Error(err))
	return fmt.Errorf("auth: %s: %w", operation, err)
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (Identity, error) {
	hash, err := HashToken(token)
	if err != nil {
		return Identity{}, ErrUnauthorized
	}
	session, err := s.sessions.GetByTokenHash(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return Identity{}, ErrUnauthorized
	}
	if err != nil {
		return Identity{}, s.failure("find session", err)
	}
	if session.RevokedAt != nil || !session.ExpiresAt.After(s.now()) {
		return Identity{}, ErrUnauthorized
	}
	user, err := s.users.GetByID(ctx, session.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return Identity{}, ErrUnauthorized
	}
	if err != nil {
		return Identity{}, s.failure("find session user", err)
	}
	if !user.IsActive {
		return Identity{}, ErrUnauthorized
	}
	user.PasswordHash = ""
	return Identity{User: user, Session: session}, nil
}

func (s *AuthService) Logout(ctx context.Context, sessionID uuid.UUID) error {
	err := s.sessions.Revoke(ctx, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return s.failure("revoke session", err)
	}
	s.log.Info("user logged out", zap.String("session_id", sessionID.String()))
	return nil
}

func newToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, errors.New("secure random token generation failed")
	}
	hash := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), hash[:], nil
}

// HashToken accepts exactly a canonical base64url encoding of 32 random bytes.
func HashToken(token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, ErrUnauthorized
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, ErrUnauthorized
	}
	hash := sha256.Sum256(raw)
	return hash[:], nil
}
