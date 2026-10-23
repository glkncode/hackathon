# Document versioning

Versioning extends the existing document service, repository and HTTP handlers.
Authentication, roles, CSRF, Origin validation and document ACL are unchanged.
The Go ingestion queue/worker is described in [ingestion-contract.md](ingestion-contract.md).
There is no Python process or public activation API in this repository.

## Migration 4

`00004_document_versioning.sql` adds version status (`uploaded`, `processing`,
`ready`, `failed`), lowercase hex SHA-256 and `updated_at`. PostgreSQL constrains
status/checksum values. `documents.active_version_id` has a composite foreign key
`(id, active_version_id) -> document_versions(document_id, id)`: a foreign
document's version cannot become active, even through direct SQL.

The existing unique B-tree `(document_id, version_number)` already supports
history ordering/number allocation; no duplicate index is added. A unique
`(document_id, id)` constraint supports the composite foreign key.

Migrations 1..3 are unchanged. Existing documents select their highest existing
version as active, preserving former download behavior. Existing version 1 still
opens/downloads. All existing statuses are uploaded, never ready; updated_at is
initialized from created_at. Originals, users, sessions and ACL are preserved.

SQL cannot read private disk originals, so legacy checksums remain NULL and are
omitted from JSON. Every new upload receives a streaming SHA-256. A future trusted
maintenance operation can hash existing files through confined storage and
backfill missing values. There is no fake checksum or automatic indexing claim.
Down removes only new metadata/constraints, retaining all versions and originals.
It also removes the active-pointer distinction: old code downloads the highest
numbered version again.

## Upload and publication

Each upload saves a new original under an independent random key. Existing file
bytes, filenames, sizes and checksums are never changed by version operations.
Only processing status/updated_at are mutable. Creating a version locks its
document with SELECT FOR UPDATE, allocates MAX(version_number)+1 and inserts in
one transaction. Concurrent uploads serialize per document, backed by uniqueness.
Identity, number and uploaded status are assigned by the server.

The first original is immediately the active **file**, with status uploaded,
preserving downloads before indexing. Later uploads leave an existing active
version unchanged. Both active and historical uploaded originals can be downloaded
with document ACL. File availability and indexing readiness are separate concepts.

Trusted in-process operations remain available for maintenance:

```go
err := documents.UpdateVersionStatus(ctx, documentID, versionID, domain.VersionProcessing)
// Maintenance only; the worker uses the atomic fenced completion transaction.
err = documents.UpdateVersionStatus(ctx, documentID, versionID, domain.VersionReady)
err = documents.PublishReadyVersion(ctx, documentID, versionID)
```

These operations have no public HTTP routes. Publication locks parent and target
version, checks ownership and ready status, then changes the active pointer within
one transaction. Concurrent status writes cannot slip between check/update.
An unknown/mismatched pair returns ErrNotFound; a non-ready target returns
ErrInvalidInput, leaving activation unchanged. Re-publishing a ready version is
safe. Status changes alone never activate a version. The ingestion worker uses
one fenced transaction for chunks, ready, monotonic publication and job completion.
Each new version is enqueued in its creation transaction.

## HTTP contract

Every endpoint requires an active authenticated user. Members require parent
document ACL for every read; admins can read all versions. Unknown/denied parents
and mismatched parent/version pairs return the same 404 JSON.

| Method/path | Permission | Success |
| --- | --- | --- |
| POST `/api/v1/documents/{id}/versions` | Admin, trusted Origin, valid CSRF | 201: new version |
| GET `/api/v1/documents/{id}/versions?limit=20&offset=0` | Admin or document ACL | 200: `{items: [...], total: N}`, newest number first |
| GET `/api/v1/documents/{id}/versions/{versionId}` | Admin or document ACL | 200: version metadata |
| GET `/api/v1/documents/{id}/versions/{versionId}/download` | Admin or document ACL | 200: original attachment, supports ranges |

POST accepts multipart/form-data containing exactly one file and no other fields.
Client version_number, status and activation fields are rejected. Existing
PDF/DOCX/TXT validation, size limits and filename safety are reused. Default file
limit is 20 MiB via DOCUMENT_MAX_UPLOAD_BYTES. Pagination uses limit 1..100 and
offset 0..1000000, default 20/0.

Version JSON: id, document_id, version_number, original_filename, mime_type,
size_bytes, created_at, updated_at, status, nullable ingestion_job_id and sha256
when available. Storage keys
and filesystem paths never appear in public responses.

Existing GET `/api/v1/documents` keeps SQL ACL filtering/pagination and adds
active_version_id. GET `/api/v1/documents/{id}` retains latest_version (highest
number), and adds active_version and active_version_id. They can differ.
GET `/api/v1/documents/{id}/download` returns the active original.

Errors: 400 invalid multipart/format/pagination; 401 missing/invalid/inactive
session; 403 role/CSRF/Origin; 404 unknown/unavailable/mismatched parent/version;
413 too large; 500 storage/database error. Revoking document ACL affects all
subsequent history/download requests and preserves the user's session. Already
authorized downloads in progress are not interrupted.

## Requests and startup

Start with the existing environment settings; migration 4 runs automatically:

```powershell
$env:DATABASE_URL = 'postgres://app:password@localhost:5432/ai_assistant?sslmode=disable'
$env:ADMIN_PASSWORD = '<unique password of at least 12 characters>'
$env:DOCUMENT_STORAGE_DIR = 'data/documents'
go run ./cmd/server
```

See [auth.md](auth.md) for login/cookies and production TLS. Bash/curl examples
assume admin.cookies and the admin CSRF cookie. Keep cookie files private.

```bash
BASE=http://localhost:8080
DOC='<document UUID>'
CSRF='<admin ai_csrf cookie>'

curl -i -b admin.cookies "$BASE/api/v1/documents/$DOC/versions" \
  -H 'Origin: http://localhost:8080' -H "X-CSRF-Token: $CSRF" \
  -F 'file=@updated-policy.pdf'

curl -i -b admin.cookies "$BASE/api/v1/documents/$DOC/versions?limit=20&offset=0"

VERSION='<version UUID>'
curl -i -b admin.cookies "$BASE/api/v1/documents/$DOC/versions/$VERSION"
curl -b admin.cookies "$BASE/api/v1/documents/$DOC/versions/$VERSION/download" \
  --output historical.pdf

# Still the previous active original until ready publication.
curl -b admin.cookies "$BASE/api/v1/documents/$DOC/download" --output active.pdf
```

## Python contract

The worker streams the exact original through the authenticated private multipart
endpoint documented in [ingestion-contract.md](ingestion-contract.md). The request
contains document_id, document_version_id, mime_type and file; storage keys never
leave Go. Go validates the version-scoped response and owns retries/publication.

Server-side retrieval must apply document ACL before fragments reach Python/LLM.
An active uploaded/processing/failed version is not searchable: search also needs
ready. Results must not mix versions. Never send prohibited fragments to a model
intending to remove them from its answer later. Shared ACL/citation rules are in
[documents.md](documents.md).

## Recovery and limits

Save/validation/hash/sync precede database insertion. A database error triggers
removal of the new original, leaving prior files intact. Disk and PostgreSQL do
not share a transaction: crashes may leave orphan files; ambiguous commits or
storage/directory-sync failures can leave missing files referenced by metadata.

Recovery: stop uploads or take a coordinated snapshot; compare database keys with
confined storage, checking size/SHA-256 when known; quarantine unmatched files
until commit outcomes are established; restore missing originals from coordinated
backups. Do not delete files merely because status is not ready. Normal uploads
and publication never delete historical originals. Automated reconciliation,
garbage collection and a legacy-checksum backfill command are not implemented.

There is no Python implementation, extraction, RAG, embedding or search API. The
Go worker persists chunks returned by the future Python service. Multiple instances need persistent
shared storage. No public activation/status or deletion API is added.

## Verification

```powershell
go mod tidy
go test ./...
go vet ./...
go build ./cmd/server
$env:TEST_DATABASE_URL = 'postgres://test:password@localhost:5432/versioning_test?sslmode=disable'
go test ./... -count=1
go test -race ./internal/service ./internal/transport/http/...
```

Integration tests use only explicit disposable TEST_DATABASE_URL ending in _test
and isolated owned schemas. They cover concurrent HTTP uploads, history/numbering,
originals/checksums, ACL/CSRF/Origin, foreign-version denials, ready publication and
its database constraint, migration-3 upgrades, repeated Up and restart persistence.
