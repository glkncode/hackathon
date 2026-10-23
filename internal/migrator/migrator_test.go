package migrator

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"ai-assistant/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func disconnectedDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig("postgres://localhost/unused_test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db // No connection is opened until an operation is attempted.
}

func TestEmbeddedSources(t *testing.T) {
	// A changed cwd must not affect discovery of embedded SQL.
	t.Chdir(t.TempDir())
	p, err := newProvider(disconnectedDB(t), migrations.FS())
	if err != nil {
		t.Fatal(err)
	}
	sources := p.ListSources()
	if len(sources) == 0 || sources[0].Version != 1 || sources[0].Path != "00001_init.sql" {
		t.Fatalf("embedded baseline not discovered: %v", sources)
	}
	foundUsersSessions := false
	for _, source := range sources {
		if _, err := fs.ReadFile(migrations.FS(), source.Path); err != nil {
			t.Fatal(err)
		}
		if source.Version == 2 && source.Path == "00002_create_users_sessions.sql" {
			foundUsersSessions = true
		}
	}
	if !foundUsersSessions {
		t.Fatal("embedded users/sessions migration not discovered")
	}
}

func TestNewVersionsDiscoveredInOrder(t *testing.T) {
	first := fstest.MapFS{"00001_init.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;")}}
	db := disconnectedDB(t)
	p, err := newProvider(db, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ListSources()) != 1 {
		t.Fatal("expected one source")
	}
	second := fstest.MapFS{
		"20261023220001_second.sql": {Data: first["00001_init.sql"].Data},
		"00001_init.sql":            first["00001_init.sql"],
	}
	p, err = newProvider(db, second)
	if err != nil {
		t.Fatal(err)
	}
	sources := p.ListSources()
	if len(sources) != 2 || sources[0].Version != 1 || sources[1].Version != 20261023220001 {
		t.Fatalf("new sources not sorted: %v", sources)
	}
}

func TestInvalidProvider(t *testing.T) {
	if _, err := newProvider(nil, migrations.FS()); err == nil {
		t.Fatal("nil pool must fail")
	}
	if _, err := newProvider(disconnectedDB(t), fstest.MapFS{}); err == nil {
		t.Fatal("empty migration sources must fail")
	}
	if err := Up(context.Background(), nil, nil); err == nil {
		t.Fatal("nil logger must fail")
	}
}

func TestCanceledOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db := disconnectedDB(t)
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)
	if err := Up(ctx, db, log); !errors.Is(err, context.Canceled) {
		t.Fatalf("Up error = %v", err)
	}
	if logs.FilterMessage("database migration failed").Len() != 1 {
		t.Fatal("migration failure must be logged")
	}
	if _, err := Status(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("Status error = %v", err)
	}
	if _, err := Version(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("Version error = %v", err)
	}
	if err := Check(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check error = %v", err)
	}
	if err := Down(ctx, db, log); !errors.Is(err, context.Canceled) {
		t.Fatalf("Down error = %v", err)
	}
}

func TestBoundedContext(t *testing.T) {
	ctx, cancel := boundedContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Minute || time.Until(deadline) < 59*time.Second {
		t.Fatal("missing default migration timeout")
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer parentCancel()
	child, childCancel := boundedContext(parent)
	defer childCancel()
	want, _ := parent.Deadline()
	got, _ := child.Deadline()
	if !got.Equal(want) {
		t.Fatal("explicit caller timeout must be preserved")
	}
}
