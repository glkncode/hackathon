package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/database"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/migrator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

// Like migrator integration tests, these require an explicit disposable database
// ending in _test. Never fall back to DATABASE_URL. Only owned schemas are dropped.
func integrationDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integration test: set TEST_DATABASE_URL to a separate PostgreSQL database ending in _test")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL (value omitted)")
	}
	if !strings.HasSuffix(parsed.Database, "_test") {
		t.Fatal("refusing integration tests: database name must end in _test")
	}
	cfg := database.Config{URL: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnectTimeout: 5 * time.Second}
	admin, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Errorf("close test admin pool: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	schema := fmt.Sprintf("repository_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop owned test schema: %v", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	cfg.URL = u.String()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test pool: %v", err)
		}
	})
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func createTestUser(t *testing.T, ctx context.Context, db *sql.DB, username string) domain.User {
	t.Helper()
	user, err := NewUserRepository(db).Create(ctx, domain.User{Username: username, PasswordHash: "test-password-hash", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func testHash(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func createTestSession(t *testing.T, ctx context.Context, db *sql.DB, userID uuid.UUID, label string) domain.Session {
	t.Helper()
	session, err := NewSessionRepository(db).Create(ctx, domain.Session{
		UserID: userID, TokenHash: testHash(label), ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestIntegrationUserLifecycle(t *testing.T) {
	db, ctx := integrationDB(t)
	r := NewUserRepository(db)
	inputID := uuid.New()
	user, err := r.Create(ctx, domain.User{ID: inputID, Username: "Alice", PasswordHash: "first-hash", Role: domain.RoleAdmin, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID == uuid.Nil || user.ID == inputID || user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() || user.Role != domain.RoleAdmin {
		t.Fatal("PostgreSQL-generated/default fields were not returned")
	}
	byID, err := r.GetByID(ctx, user.ID)
	if err != nil || byID != user {
		t.Fatalf("GetByID did not round-trip user: %v", err)
	}
	byName, err := r.GetByUsername(ctx, "aLiCe")
	if err != nil || byName.ID != user.ID {
		t.Fatalf("case-insensitive lookup failed: %v", err)
	}
	_, err = r.Create(ctx, domain.User{Username: "ALICE", PasswordHash: "duplicate-private-hash"})
	if !errors.Is(err, domain.ErrAlreadyExists) || strings.Contains(err.Error(), "duplicate-private-hash") {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}
	if err := r.UpdatePasswordHash(ctx, user.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	updated, err := r.GetByID(ctx, user.ID)
	if err != nil || updated.PasswordHash != "new-hash" || !updated.UpdatedAt.After(user.UpdatedAt) {
		t.Fatalf("password update or updated_at failed: %v", err)
	}
	if err := r.SetActive(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}
	inactive, err := r.GetByID(ctx, user.ID)
	if err != nil || inactive.IsActive || !inactive.UpdatedAt.After(updated.UpdatedAt) {
		t.Fatalf("deactivation or updated_at failed: %v", err)
	}
	if err := r.SetActive(ctx, user.ID, false); err != nil {
		t.Fatalf("updating an unchanged existing user must succeed: %v", err)
	}
	if err := r.UpdatePasswordHash(ctx, user.ID, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("empty password hash update accepted: %v", err)
	}
	stillValid, err := r.GetByID(ctx, user.ID)
	if err != nil || stillValid.PasswordHash != "new-hash" {
		t.Fatal("rejected update changed stored hash")
	}
}

func TestIntegrationUserNotFound(t *testing.T) {
	db, ctx := integrationDB(t)
	r := NewUserRepository(db)
	id := uuid.New()
	checks := []func() error{
		func() error { _, err := r.GetByID(ctx, id); return err },
		func() error { _, err := r.GetByUsername(ctx, "unknown"); return err },
		func() error { return r.UpdatePasswordHash(ctx, id, "hash") },
		func() error { return r.SetActive(ctx, id, false) },
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	}
}

func TestIntegrationUserConstraintsAndDefaults(t *testing.T) {
	db, ctx := integrationDB(t)
	r := NewUserRepository(db)
	for _, input := range []domain.User{
		{Username: "", PasswordHash: "hash"},
		{Username: "   ", PasswordHash: "hash"},
		{Username: "\t\n", PasswordHash: "hash"},
		{Username: strings.Repeat("a", 65), PasswordHash: "hash"},
		{Username: "empty-hash", PasswordHash: ""},
		{Username: "wrong-role", PasswordHash: "hash", Role: "owner"},
	} {
		if _, err := r.Create(ctx, input); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("invalid user accepted or incorrectly mapped: %v", err)
		}
	}
	var id uuid.UUID
	var role domain.Role
	var active bool
	if err := db.QueryRowContext(ctx, `INSERT INTO users (username, password_hash) VALUES ($1, $2)
		RETURNING id, role, is_active`, "db-defaults", "hash").Scan(&id, &role, &active); err != nil {
		t.Fatal(err)
	}
	if id == uuid.Nil || role != domain.RoleMember || !active {
		t.Fatal("database defaults not applied")
	}
	inactive, err := r.Create(ctx, domain.User{Username: "inactive", PasswordHash: "hash", IsActive: false})
	if err != nil || inactive.IsActive || inactive.Role != domain.RoleMember {
		t.Fatal("repository must preserve explicit inactive flag and default member role")
	}
	injection := `name' OR '1'='1`
	if _, err := r.GetByUsername(ctx, injection); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("username was not bound as a parameter: %v", err)
	}
}

func TestIntegrationSessionLifecycle(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "session-owner")
	r := NewSessionRepository(db)
	input := domain.Session{
		ID: uuid.New(), UserID: user.ID, TokenHash: testHash("first"), CSRFTokenHash: testHash("csrf"),
		CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	session, err := r.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == uuid.Nil || session.ID == input.ID || session.CreatedAt.IsZero() || !session.CreatedAt.After(input.CreatedAt) || !bytes.Equal(session.CSRFTokenHash, input.CSRFTokenHash) || session.RevokedAt != nil {
		t.Fatal("session fields/generation did not round-trip")
	}
	loaded, err := r.GetByTokenHash(ctx, input.TokenHash)
	if err != nil || loaded.ID != session.ID || loaded.UserID != user.ID || !bytes.Equal(loaded.TokenHash, input.TokenHash) {
		t.Fatalf("GetByTokenHash failed: %v", err)
	}
	if _, err := r.Create(ctx, input); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate token hash accepted: %v", err)
	}
	if err := r.Revoke(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := r.GetByTokenHash(ctx, input.TokenHash)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("Revoke did not persist timestamp: %v", err)
	}
	if err := r.Revoke(ctx, session.ID); err != nil {
		t.Fatalf("repeat Revoke failed: %v", err)
	}
	again, err := r.GetByTokenHash(ctx, input.TokenHash)
	if err != nil || again.RevokedAt == nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatal("repeat Revoke changed the original revocation timestamp")
	}
	if err := NewUserRepository(db).SetActive(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetByTokenHash(ctx, input.TokenHash); err != nil {
		t.Fatal("data lookup must not perform an authentication check")
	}
	if err := r.Revoke(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown session Revoke error = %v", err)
	}
	if _, err := r.GetByTokenHash(ctx, testHash("unknown")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown token lookup error = %v", err)
	}
}

func TestIntegrationSessionConstraints(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "constraints-owner")
	for name, mutate := range map[string]func(*domain.Session){
		"foreign key": func(s *domain.Session) { s.UserID = uuid.New() },
		"nil hash":    func(s *domain.Session) { s.TokenHash = nil },
		"empty hash":  func(s *domain.Session) { s.TokenHash = []byte{} },
		"short hash":  func(s *domain.Session) { s.TokenHash = make([]byte, 31) },
		"long hash":   func(s *domain.Session) { s.TokenHash = make([]byte, 33) },
		"empty csrf":  func(s *domain.Session) { s.CSRFTokenHash = []byte{} },
		"short csrf":  func(s *domain.Session) { s.CSRFTokenHash = make([]byte, 31) },
		"long csrf":   func(s *domain.Session) { s.CSRFTokenHash = make([]byte, 33) },
		"expired":     func(s *domain.Session) { s.ExpiresAt = time.Now().Add(-time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			session := domain.Session{UserID: user.ID, TokenHash: testHash(name), ExpiresAt: time.Now().Add(time.Hour)}
			mutate(&session)
			if _, err := NewSessionRepository(db).Create(ctx, session); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("constraint not enforced/mapped: %v", err)
			}
		})
	}
}

func TestIntegrationSessionBulkOperations(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "bulk-owner")
	otherUser := createTestUser(t, ctx, db, "other-owner")
	r := NewSessionRepository(db)
	first := createTestSession(t, ctx, db, user.ID, "first")
	second := createTestSession(t, ctx, db, user.ID, "second")
	expired := createTestSession(t, ctx, db, user.ID, "expired")
	previouslyRevoked := createTestSession(t, ctx, db, user.ID, "revoked")
	other := createTestSession(t, ctx, db, otherUser.ID, "other")
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET created_at = NOW() - INTERVAL '2 hours',
		expires_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Revoke(ctx, previouslyRevoked.ID); err != nil {
		t.Fatal(err)
	}
	oldRevoked, err := r.GetByTokenHash(ctx, previouslyRevoked.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RevokeAllForUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	for _, session := range []domain.Session{first, second} {
		loaded, err := r.GetByTokenHash(ctx, session.TokenHash)
		if err != nil || loaded.RevokedAt == nil {
			t.Fatalf("active session not revoked: %v", err)
		}
	}
	for _, session := range []domain.Session{expired, other} {
		loaded, err := r.GetByTokenHash(ctx, session.TokenHash)
		if err != nil || loaded.RevokedAt != nil {
			t.Fatalf("expired/other user's session unexpectedly changed: %v", err)
		}
	}
	unchanged, err := r.GetByTokenHash(ctx, previouslyRevoked.TokenHash)
	if err != nil || unchanged.RevokedAt == nil || oldRevoked.RevokedAt == nil || !unchanged.RevokedAt.Equal(*oldRevoked.RevokedAt) {
		t.Fatal("bulk revoke changed an earlier revocation")
	}
	if err := r.RevokeAllForUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.RevokeAllForUser(ctx, uuid.New()); err != nil {
		t.Fatal("bulk revoke for unknown user must be a no-op")
	}
	if count, err := r.DeleteExpired(ctx); err != nil || count != 1 {
		t.Fatalf("DeleteExpired = %d, %v", count, err)
	}
	if _, err := r.GetByTokenHash(ctx, expired.TokenHash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("expired session was not deleted")
	}
	if _, err := NewUserRepository(db).GetByID(ctx, user.ID); err != nil {
		t.Fatal("deleting sessions must not delete the user")
	}
	if count, err := r.DeleteExpired(ctx); err != nil || count != 0 {
		t.Fatalf("second DeleteExpired = %d, %v", count, err)
	}
	if _, err := r.GetByTokenHash(ctx, other.TokenHash); err != nil {
		t.Fatal("unexpired session was deleted")
	}
}

func TestIntegrationUserDeletionCascadesSessions(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "cascade-owner")
	session := createTestSession(t, ctx, db, user.ID, "cascade")
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSessionRepository(db).GetByTokenHash(ctx, session.TokenHash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ON DELETE CASCADE did not remove session: %v", err)
	}
}

func TestIntegrationUsersSessionsMigrationLifecycle(t *testing.T) {
	db, ctx := integrationDB(t)
	user := createTestUser(t, ctx, db, "restart-owner")
	session := createTestSession(t, ctx, db, user.ID, "restart")
	statuses, err := migrator.Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, status := range statuses {
		if status.Source.Version == 2 && status.Source.Path == "00002_create_users_sessions.sql" && status.State == goose.StateApplied {
			found = true
		}
	}
	if !found {
		t.Fatal("Goose did not discover/apply migration 2")
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id = 2 AND is_applied`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration 2 reapplied on restart: %d, %v", count, err)
	}
	after, err := NewUserRepository(db).GetByID(ctx, user.ID)
	if err != nil || after != user {
		t.Fatalf("user data changed across restart: %v", err)
	}
	afterSession, err := NewSessionRepository(db).GetByTokenHash(ctx, session.TokenHash)
	if err != nil || afterSession.ID != session.ID || !bytes.Equal(afterSession.TokenHash, session.TokenHash) {
		t.Fatalf("session data changed across restart: %v", err)
	}
	if err := migrator.Down(ctx, db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "sessions"} {
		var name sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1)::text`, table).Scan(&name); err != nil || name.Valid {
			t.Fatalf("Down did not remove %s: %v", table, err)
		}
	}
	version, err := migrator.Version(ctx, db)
	if err != nil || version != 1 {
		t.Fatalf("Down must leave baseline migration applied: %d, %v", version, err)
	}
	if err := migrator.Up(ctx, db, zap.NewNop()); err != nil {
		t.Fatalf("Up after Down failed: %v", err)
	}
}
