// Package migrator runs embedded Goose migrations using the caller's pool.
package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"ai-assistant/migrations"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"go.uber.org/zap"
)

// newProvider is also used with isolated in-memory SQL sources in integration
// tests. No global Goose settings or registries are changed.
func newProvider(db *sql.DB, sources fs.FS) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("migrator: create PostgreSQL session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sources,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		// Emit our own structured results through zap, including partial failures.
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return nil, fmt.Errorf("migrator: create Goose provider: %w", err)
	}
	// Do not call provider.Close: the pool belongs to the caller.
	return provider, nil
}

// boundedContext preserves a caller-supplied timeout (including one longer than
// a minute), and supplies a safe default for direct callers without a deadline.
func boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Minute)
}

// Up applies only pending migrations, in order, under Goose's session advisory
// lock. Migrations are transactional unless their SQL explicitly opts out using
// Goose's NO TRANSACTION directive. Up never rolls back earlier migrations.
func Up(ctx context.Context, db *sql.DB, log *zap.Logger) error {
	return up(ctx, db, migrations.FS(), log)
}

func up(ctx context.Context, db *sql.DB, sources fs.FS, log *zap.Logger) error {
	if log == nil {
		return errors.New("migrator: logger is required")
	}
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	started := time.Now()
	log.Info("starting database migrations")
	p, err := newProvider(db, sources)
	if err != nil {
		log.Error("database migration failed", zap.Error(err))
		return err
	}
	results, err := p.Up(ctx)
	if err != nil {
		fields := []zap.Field{zap.Duration("duration", time.Since(started)), zap.Error(err)}
		var partial *goose.PartialError
		if errors.As(err, &partial) {
			logApplied(log, partial.Applied)
			fields = append(fields, zap.Int64("migration_version", partial.Failed.Source.Version))
		}
		log.Error("database migration failed", fields...)
		return fmt.Errorf("migrator: up: %w", err)
	}
	logApplied(log, results)
	log.Info("database migrations completed", zap.Int("applied", len(results)), zap.Duration("duration", time.Since(started)))
	return nil
}

func logApplied(log *zap.Logger, results []*goose.MigrationResult) {
	for _, result := range results {
		log.Info("migration applied", zap.Int64("migration_version", result.Source.Version), zap.Duration("duration", result.Duration))
	}
}

// Down rolls back exactly one migration. Only explicitly requested CLI calls
// should reach this function; the application checks the production policy.
func Down(ctx context.Context, db *sql.DB, log *zap.Logger) error {
	if log == nil {
		return errors.New("migrator: logger is required")
	}
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	p, err := newProvider(db, migrations.FS())
	if err != nil {
		return err
	}
	result, err := p.Down(ctx)
	if err != nil {
		return fmt.Errorf("migrator: down: %w", err)
	}
	if result != nil {
		log.Warn("migration rolled back", zap.Int64("migration_version", result.Source.Version), zap.Duration("duration", result.Duration))
	}
	return nil
}

func Status(ctx context.Context, db *sql.DB) ([]*goose.MigrationStatus, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	p, err := newProvider(db, migrations.FS())
	if err != nil {
		return nil, err
	}
	status, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrator: status: %w", err)
	}
	return status, nil
}

func Version(ctx context.Context, db *sql.DB) (int64, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	p, err := newProvider(db, migrations.FS())
	if err != nil {
		return 0, err
	}
	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrator: version: %w", err)
	}
	return version, nil
}

// Check fails closed if migrations are pending/missing or the database is ahead
// of this binary. It never executes SQL Up/Down. Goose may initialize its own
// version metadata when checking a fresh database.
func Check(ctx context.Context, db *sql.DB) error {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	p, err := newProvider(db, migrations.FS())
	if err != nil {
		return err
	}
	return check(ctx, p)
}

func check(ctx context.Context, p *goose.Provider) error {
	statuses, err := p.Status(ctx)
	if err != nil {
		return fmt.Errorf("migrator: check schema status: %w", err)
	}
	for _, status := range statuses {
		if status.State != goose.StateApplied {
			return fmt.Errorf("migrator: schema incompatible: migration %d is pending; run migrate up", status.Source.Version)
		}
	}
	current, target, err := p.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("migrator: check schema version: %w", err)
	}
	if current != target {
		return fmt.Errorf("migrator: schema incompatible: database version %d, binary version %d", current, target)
	}
	return nil
}
