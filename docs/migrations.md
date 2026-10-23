# PostgreSQL migrations

The application uses database/sql + pgx/v5, Goose v3 Providers and embedded SQL.
It does not load `.env` files. Configure the process environment using the
settings in `.env.example`; never log or commit a real DATABASE_URL.

## Run

PowerShell example for a local development database:

```powershell
$env:DATABASE_URL = 'postgres://app:password@localhost:5432/ai_assistant?sslmode=disable'
go run ./cmd/server
```

Default startup opens and pings the connection pool, runs pending migrations
under Goose's PostgreSQL session advisory lock, then verifies schema compatibility.
Errors stop initialization with a nonzero exit code. Earlier successful
migrations remain applied if a later migration fails. Each SQL migration runs
transactionally unless it explicitly contains `-- +goose NO TRANSACTION`.
After schema verification, administrator bootstrap and the authentication HTTP
server start; see [Authentication API](auth.md). Pool and logger cleanup run
after HTTP shutdown. Explicit migration CLI commands exit without starting HTTP.

Settings (empty values use defaults, except DATABASE_URL which is required):

| Variable | Default | Meaning |
| --- | --- | --- |
| APP_ENV | development | development or production; also controls rollback policy |
| LOG_LEVEL | info | debug, info, warn, error |
| SERVICE_NAME | ai-assistant | zap service field |
| DATABASE_URL | required | PostgreSQL connection URL |
| DB_MAX_OPEN_CONNS | 10 | Positive pool size |
| DB_MAX_IDLE_CONNS | 5 | Between zero and maximum open connections |
| DB_CONNECT_TIMEOUT | 10s | Pool ping / connection deadline |
| DB_MIGRATION_TIMEOUT | 60s | Total migration/check/CLI operation deadline, including lock wait |
| AUTO_MIGRATE | true | Automatically run Up during default startup |
| ALLOW_PRODUCTION_MIGRATION_DOWN | false | Explicit opt-in for manual production rollback |

`AUTO_MIGRATE=false` skips Up, but still verifies all embedded versions are
applied and the database version matches the binary. An empty, older, or newer
schema stops startup. Run `migrate up` in a separate deployment step first.
Goose can initialize its version metadata table during status/version/check;
these operations do not execute the SQL migrations themselves.

Schema checks verify Goose version history, not schema drift or checksums.
Do not edit/delete migrations already applied to a shared database.

## Add a migration

Create a file directly in `migrations/` with a unique increasing version. Prefer
UTC timestamps; coordinate filenames if two developers create one in the same
second. Check that the version exceeds every existing migration's version.

```powershell
$version = (Get-Date).ToUniversalTime().ToString('yyyyMMddHHmmss')
$path = Join-Path 'migrations' "${version}_add_feature.sql"
if (Test-Path -LiteralPath $path) { throw 'Migration already exists' }
@'
-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
'@ | Set-Content -LiteralPath $path -Encoding utf8
```

Replace the placeholder SQL with the intended change and its explicit inverse.
Use Goose `StatementBegin` / `StatementEnd` annotations around function bodies
with internal semicolons. Out-of-order migrations are rejected by default.

Rebuild and run:

```powershell
go build -o server.exe ./cmd/server
./server.exe
```

Every `migrations/*.sql` file is embedded during build. New files need a rebuild;
the binary needs no external migrations folder and can run from any directory.
The `goose_db_version` table tracks completed versions, so restarts do not apply
them again. Ordinary startup never executes Down.

## Manual CLI

```powershell
go run ./cmd/server migrate up
go run ./cmd/server migrate status
go run ./cmd/server migrate version
go run ./cmd/server migrate down
```

`up` explicitly applies pending migrations even when AUTO_MIGRATE=false.
`status` and `version` report results as structured zap events. `down` rolls back
exactly one version, and only works in production when
ALLOW_PRODUCTION_MIGRATION_DOWN=true is explicitly set. Use the embedded CLI
for the same migration sources and advisory lock as application startup.

## Tests

```powershell
go test ./...
```

Normal unit tests require no running database. Integration tests skip with an
explanation unless TEST_DATABASE_URL selects a separate disposable PostgreSQL
database whose name ends in `_test`. Do not point it at production. Tests create
unique schemas and drop only their own schemas; the test role needs permission
to create schemas. An explicitly configured but unavailable test database fails
the tests instead of silently skipping.

```powershell
$env:TEST_DATABASE_URL = 'postgres://test:password@localhost:5432/migration_test?sslmode=disable'
go test -v ./internal/migrator -run Integration -count=1
Remove-Item Env:TEST_DATABASE_URL
```

Integration coverage includes embedded SQL from another working directory,
restarts, adding a new source set (simulating a rebuilt binary), data retention,
transactional failure, deadline cancellation, concurrent instances and advisory
lock contention. The probe tables exist only inside isolated test schemas.

## HTTP server startup

The HTTP startup at the end of `run` in `cmd/server/main.go` is reached only after
migration success and schema verification. Repositories and authentication services
share the existing `db` and `*zap.Logger`. The server uses the application `ctx`,
not the migration deadline. `run` waits for shutdown before closing the pool
and flushing logs. Migration CLI commands return before bootstrap/HTTP.
