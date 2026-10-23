# Users and document access

This module extends the existing cookie authentication. It does not add public
registration, role changes, content extraction, Python calls or indexing.

## Run

Configure the existing PostgreSQL and bootstrap settings described in [auth.md](auth.md).
The application reads process environment variables and does not load `.env`.

```powershell
$env:DATABASE_URL = 'postgres://app:password@localhost:5432/ai_assistant?sslmode=disable'
$env:ADMIN_PASSWORD = '<unique password of at least 12 characters>'
$env:DOCUMENT_STORAGE_DIR = 'data/documents'
$env:DOCUMENT_MAX_UPLOAD_BYTES = '20971520'
go run ./cmd/server
```

The defaults are a private `data/documents` directory relative to the process's
working directory and a 20 MiB file limit. The configurable maximum is 1 GiB.
Production should use an absolute persistent directory, with backups coordinated
with PostgreSQL. Only the serving process account should manage its contents.
On Unix, the directory uses mode 0700 and files use 0600. On Windows a protected,
inheritable DACL grants access to the process account and LocalSystem. The directory
must not be exposed by a reverse proxy or a static file server. Storage keys are
generated UUIDs, independent of original filenames, and never returned in JSON.
`os.Root` confines file operations, including protection against symlink escapes.

Startup still migrates/checks the database, then bootstraps the administrator and
starts HTTP. Migration commands do not initialize storage or start HTTP. Production
TLS and secure-cookie requirements are unchanged; see [auth.md](auth.md).

## Migration and persistence

`00003_documents_access.sql` creates:

- `documents`: the logical document, title, creator and timestamps.
- `document_versions`: originals and metadata; unique `(document_id, version_number)`.
- `document_access`: explicit user grants; unique `(document_id, user_id)` and
  indexes on document/user IDs. Grants reference the document, not a version.

All relationships have foreign keys. Existing users, sessions and migrations 1/2
are unchanged. Up is repeatable through Goose; Down drops only the three new
tables, without cascading into users or sessions. Back up document metadata
before explicitly rolling back migration 3. Down does not delete original files.

Creating a document and version 1 uses one database transaction. Uploads are bounded,
written to a private file, checked and synced before metadata creation. Invalid or
oversized uploads are removed; metadata errors trigger file removal. File storage
and PostgreSQL are not a distributed transaction: process crashes or ambiguous
database commit failures require reconciliation of stored originals and metadata.
No automatic reconciliation/garbage collection is provided yet.

Migration 4 extends this schema with version status, checksum and an active-file
pointer. Historical uploads and ready-only publication are documented in
[versioning.md](versioning.md).

## API

Every endpoint requires an active authenticated user. Every POST/DELETE also
requires the trusted `Origin` and `X-CSRF-Token` from that user's readable CSRF
cookie. The HttpOnly session cookie is sent with `credentials: "include"` in a
browser. Credentialed CORS permits explicit trusted origins, including DELETE
preflights. No wildcard CORS is used.

Pagination: `limit=20&offset=0` by default; limit 1..100, offset 0..1000000.
Invalid, repeated or malformed pagination values return 400. Lists return
`{"items":[],"total":0}`, including empty pages with their actual total.

| Method/path | Who | Success |
| --- | --- | --- |
| POST `/api/v1/users` | Admin | 201: `{id, username, role, is_active}` |
| GET `/api/v1/users?limit=20&offset=0` | Admin | 200: paginated public users |
| POST `/api/v1/documents` | Admin | 201: document metadata with `latest_version` |
| GET `/api/v1/documents?limit=20&offset=0` | Admin/member | 200: paginated document metadata |
| GET `/api/v1/documents/{id}` | Admin or explicit ACL | 200: document metadata with `latest_version` |
| GET `/api/v1/documents/{id}/download` | Admin or explicit ACL | 200: original attachment; HTTP range requests supported |
| GET `/api/v1/documents/{id}/access?limit=20&offset=0` | Admin | 200: paginated public users with grants |
| POST `/api/v1/documents/{id}/access` | Admin | 201: grant `{id, document_id, user_id, granted_by, created_at}` |
| DELETE `/api/v1/documents/{id}/access/{userId}` | Admin | 204: idempotent revocation |

Public user fields are exactly `id`, `username`, `role`, `is_active`. Password
hashes and session information are excluded. Document metadata contains `id`,
`title`, `created_by`, `created_at`, `updated_at`, `active_version_id`. The
`latest_version` and `active_version` objects contain `id`, `document_id`,
`version_number`, `original_filename`, `mime_type`, `size_bytes`, `created_at`,
`updated_at`, `status`, and `sha256` when known. Neither contains `storage_key`
or a filesystem path. Ordinary downloads return the active version; see
[versioning.md](versioning.md) for history and publication.
The list endpoint returns document metadata without version details.

User creation accepts exactly `{"username":"employee1","password":"..."}`.
The server always creates an active member. A client-supplied `role` is rejected.
Usernames require 3..64 ASCII letters, digits, `_`, `-`, `.`; uniqueness is
case-insensitive in PostgreSQL. Passwords use existing Argon2id: at least 12
Unicode characters, at most 1024 UTF-8 bytes. Duplicate usernames return 409.
Bootstrap is unchanged and remains the only way to provision the initial admin.

Upload accepts multipart/form-data containing exactly one `title` and one `file`.
Titles require 1..255 Unicode characters after trimming; controls are rejected.
Filename path components are discarded and are never used as storage paths.
The file limit excludes multipart overhead; total request size is limited to
the configured file limit plus 1 MiB, with at most 1 MiB retained in memory by
multipart parsing. Temporary multipart files are removed after handling.

Only PDF, DOCX and UTF-8 TXT are accepted. Client MIME types are ignored. Extension
and content must agree: PDF header/version and EOF marker; DOCX ZIP structure,
WordprocessingML root and OOXML content type; TXT valid UTF-8 without binary
control characters. DOCX checks also bound ZIP entry counts/expanded sizes and
reject duplicate or unsafe archive paths. These identify formats, without
extracting/indexing content. They are not full PDF/Office validation or malware
scanning. Files are downloaded as attachments with server-selected MIME,
`nosniff`, no-store and a sandbox CSP.

Errors use `{"error":"message"}`: 400 invalid input/format; 401 missing, expired,
revoked or inactive authentication; 403 administrative role, CSRF or Origin
denial; 404 unknown/unavailable document or user; 409 duplicate username/grant;
413 file/request too large; 500 internal/storage failure. Unavailable and unknown
documents produce the same 404 response. Repeated DELETE returns 204 when the
document exists, even when that grant is already absent. Unknown documents return
404. Grants can target only existing active members; inactive users/admin targets
return 400. Invalid document UUIDs return 404; invalid target user UUIDs return 400.

## Request examples

The following examples use Bash/curl. Replace passwords and IDs with actual values.
The first response supplies `ai_csrf`; copy it into `ADMIN_CSRF`. Use
`__Host-ai_csrf` with HTTPS secure cookies.

```bash
BASE=http://localhost:8080
ORIGIN=http://localhost:8080

curl -i -c admin.cookies "$BASE/api/v1/auth/login" \
  -H "Origin: $ORIGIN" -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<admin password>"}'

ADMIN_CSRF='<admin ai_csrf cookie>'

curl -i -b admin.cookies "$BASE/api/v1/users" \
  -H "Origin: $ORIGIN" -H "X-CSRF-Token: $ADMIN_CSRF" \
  -H 'Content-Type: application/json' \
  --data '{"username":"employee1","password":"<unique member password>"}'

curl -i -b admin.cookies "$BASE/api/v1/users?limit=20&offset=0"

curl -i -b admin.cookies "$BASE/api/v1/documents" \
  -H "Origin: $ORIGIN" -H "X-CSRF-Token: $ADMIN_CSRF" \
  -F 'title=Инструкция по документообороту' -F 'file=@example.pdf'

DOCUMENT_ID='<uploaded document id>'
USER_ID='<created member id>'

curl -i -b admin.cookies "$BASE/api/v1/documents/$DOCUMENT_ID/access" \
  -H "Origin: $ORIGIN" -H "X-CSRF-Token: $ADMIN_CSRF" \
  -H 'Content-Type: application/json' --data "{\"user_id\":\"$USER_ID\"}"

curl -i -c member.cookies "$BASE/api/v1/auth/login" \
  -H "Origin: $ORIGIN" -H 'Content-Type: application/json' \
  --data '{"username":"employee1","password":"<unique member password>"}'

curl -i -b member.cookies "$BASE/api/v1/documents"
curl -b member.cookies "$BASE/api/v1/documents/$DOCUMENT_ID/download" \
  --output downloaded.pdf

curl -i -b admin.cookies -X DELETE \
  "$BASE/api/v1/documents/$DOCUMENT_ID/access/$USER_ID" \
  -H "Origin: $ORIGIN" -H "X-CSRF-Token: $ADMIN_CSRF"

# Now 404; member session remains valid.
curl -i -b member.cookies "$BASE/api/v1/documents/$DOCUMENT_ID"
```

Cookie files contain credentials; keep them private and delete after use.

## Authorization and retrieval

Admin sees/downloads all documents and controls grants. Members have no implicit
access, including to a document creator's files. Their lists and totals are filtered
by ACL inside SQL. Services authorize individual metadata, versions and downloads
against the parent document, before opening its file. Sessions are checked for user
activity on each request. Revocation is not cached: subsequent requests lose access
immediately. An already authorized download in progress is not interrupted.

The [Search API](search.md) applies the same ACL in SQL and searches only active
ready versions. It returns persisted fragments and version-bound citations.

All fragments, citations, search results and future LLM answers must respect
`document_access`. Go must not send Python content from documents unavailable to
the current user. Retrieval must filter permitted `document_id` values on the
server before any content reaches a model. Global retrieval followed by removing
forbidden text from an already generated answer is prohibited. Every citation
must refer to a document accessible to the requesting user.

## Verification and limits

```powershell
go mod tidy
go test ./...
go vet ./...
go build ./cmd/server
$env:TEST_DATABASE_URL = 'postgres://test:password@localhost:5432/documents_test?sslmode=disable'
go test ./... -count=1
go test -race ./internal/service ./internal/transport/http/...
```

Unit tests need no database. Integration tests use only explicit TEST_DATABASE_URL,
require a database name ending in `_test`, and own isolated disposable schemas.
They cover user creation/login, upload/grant/download/revoke, denied direct
requests, inactive users, CSRF, pagination, unique grants under concurrency,
foreign keys, transactional document/version creation, Goose Up/Down and preservation
of user/session history. Never point these tests at a production database.

This release supports version history and local original storage.
Multi-instance deployments require shared storage. There are no
document deletion, storage reconciliation, antivirus, indexing,
text extraction, RAG or account/role editing APIs. Login rate limits remain
per process; HTTPS deployment details and cookie host constraints are in [auth.md](auth.md).
