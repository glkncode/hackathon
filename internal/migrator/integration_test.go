package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ai-assistant/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Integration tests require an explicitly selected disposable database with a
// name ending in _test. Each test owns only its randomly named schema.
func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integration test: set TEST_DATABASE_URL to a separate PostgreSQL database ending in _test")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL cannot be parsed (value omitted)")
	}
	if !strings.HasSuffix(parsed.Database, "_test") {
		t.Fatal("refusing integration tests: database name must end in _test")
	}
	cfg := database.Config{URL: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnectTimeout: 5 * time.Second}
	admin, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open explicit test database: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Errorf("close test admin pool: %v", err)
		}
	})
	schema := fmt.Sprintf("migrator_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("remove owned test schema: %v", err)
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
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test pool: %v", err)
		}
	})
	if db.Stats().MaxOpenConnections != cfg.MaxOpenConns {
		t.Fatal("pool maximum was not configured")
	}
	return db
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestIntegrationEmbeddedLifecycle(t *testing.T) {
	db := integrationDB(t)
	ctx := testContext(t)
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)
	if err := Check(ctx, db); err == nil {
		t.Fatal("fresh schema must not be declared compatible when auto-migrate is disabled")
	}
	t.Chdir(t.TempDir())
	if err := Up(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, db); err != nil {
		t.Fatal(err)
	}
	version, err := Version(ctx, db)
	if err != nil || version < 1 {
		t.Fatalf("unexpected version: %d, %v", version, err)
	}
	statuses, err := Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.State != goose.StateApplied {
			t.Fatal("pending migration after Up")
		}
	}
	before := logs.FilterMessage("migration applied").Len()
	if before < 1 {
		t.Fatal("applied migration not logged")
	}
	if err := Up(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if got := logs.FilterMessage("migration applied").Len(); got != before {
		t.Fatal("migration reapplied on restart")
	}
	if err := Down(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, db); err == nil {
		t.Fatal("rollback must make schema incompatible")
	}
	if err := Up(ctx, db, log); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationNewVersionPreservesData(t *testing.T) {
	db := integrationDB(t)
	ctx := testContext(t)
	first := fstest.MapFS{"00001_probe.sql": {Data: []byte(`-- +goose Up
CREATE TABLE migration_probe (id integer PRIMARY KEY, value text NOT NULL);
INSERT INTO migration_probe VALUES (1, 'original');
-- +goose Down
DROP TABLE migration_probe;
`)}}
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)
	if err := up(ctx, db, first, log); err != nil {
		t.Fatal(err)
	}
	// Simulate restart with the original binary: the insert would fail if run twice.
	if err := up(ctx, db, first, log); err != nil {
		t.Fatal(err)
	}
	second := fstest.MapFS{
		"00001_probe.sql": first["00001_probe.sql"],
		"20261023220001_probe.sql": {Data: []byte(`-- +goose Up
INSERT INTO migration_probe VALUES (2, 'new');
-- +goose Down
DELETE FROM migration_probe WHERE id = 2;
`)},
	}
	p, err := newProvider(db, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, p); err == nil {
		t.Fatal("binary with a new migration must reject the old schema")
	}
	if err := up(ctx, db, second, log); err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := up(ctx, db, second, log); err != nil {
		t.Fatal(err)
	}
	if logs.FilterMessage("migration applied").Len() != 2 {
		t.Fatal("each migration must be applied exactly once")
	}
	var value string
	if err := db.QueryRowContext(ctx, "SELECT value FROM migration_probe WHERE id=1").Scan(&value); err != nil || value != "original" {
		t.Fatalf("original data changed: %q, %v", value, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM migration_probe").Scan(&count); err != nil || count != 2 {
		t.Fatalf("new data missing: count=%d, %v", count, err)
	}
	oldBinary, err := newProvider(db, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, oldBinary); err == nil {
		t.Fatal("old binary must reject schema ahead of its embedded sources")
	}
}

func TestIntegrationFailureIsTransactional(t *testing.T) {
	db := integrationDB(t)
	ctx := testContext(t)
	bad := fstest.MapFS{
		"00001_init.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;")},
		"00002_bad.sql": {Data: []byte(`-- +goose Up
CREATE TABLE should_rollback (id integer);
SELECT * FROM table_that_does_not_exist;
-- +goose Down
SELECT 1;
`)},
	}
	core, logs := observer.New(zap.InfoLevel)
	err := up(ctx, db, bad, zap.New(core))
	var partial *goose.PartialError
	if !errors.As(err, &partial) || partial.Failed.Source.Version != 2 {
		t.Fatalf("expected failed migration 2: %v", err)
	}
	if logs.FilterMessage("database migration failed").Len() != 1 || logs.FilterMessage("migration applied").Len() != 1 {
		t.Fatal("failure and previous successful migration must be logged")
	}
	var table sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('should_rollback')::text").Scan(&table); err != nil || table.Valid {
		t.Fatalf("failed migration was not rolled back: %v, %v", table, err)
	}
	version, err := Version(ctx, db)
	if err != nil || version != 1 {
		t.Fatalf("successful earlier migration must remain applied: %d, %v", version, err)
	}
}

func TestIntegrationMigrationTimeout(t *testing.T) {
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	slow := fstest.MapFS{"00001_slow.sql": {Data: []byte("-- +goose Up\nSELECT pg_sleep(10);\n-- +goose Down\nSELECT 1;")}}
	started := time.Now()
	err := up(ctx, db, slow, zap.NewNop())
	if err == nil || ctx.Err() != context.DeadlineExceeded || time.Since(started) > 5*time.Second {
		t.Fatalf("migration ignored its deadline: %v, elapsed=%s", err, time.Since(started))
	}
	if err := Up(testContext(t), db, zap.NewNop()); err != nil {
		t.Fatalf("timeout left an unrecoverable pool or advisory lock: %v", err)
	}
}

func TestIntegrationConcurrentInstances(t *testing.T) {
	db := integrationDB(t)
	ctx := testContext(t)
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() { errorsCh <- Up(ctx, db, zap.NewNop()) }()
	}
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=1 AND is_applied").Scan(&count); err != nil || count != 1 {
		t.Fatalf("baseline applied more than once: %d, %v", count, err)
	}
	if err := Check(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationLockHonorsDeadline(t *testing.T) {
	db := integrationDB(t)
	ctx := testContext(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Hold Goose's documented lock to emulate a different application instance.
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", int64(4097083626)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", int64(4097083626)); err != nil {
			t.Errorf("release test lock: %v", err)
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := Up(deadline, db, zap.NewNop()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock acquisition must honor timeout: %v", err)
	}
}
