# Document ingestion: Go ↔ Python

Go owns originals, version metadata, ACL, jobs, retries, chunks and publication.
Python extracts text and returns version-scoped chunks. This repository implements
only the Go side. There is no parser, Python server, embedding, LLM or search API.

## Starting workers

Configure the environment (the application does not load `.env` files):

```dotenv
AI_SERVICE_URL=http://127.0.0.1:8000
AI_SERVICE_TOKEN=<shared private secret>
INGESTION_ENABLED=true
INGESTION_WORKERS=1
INGESTION_POLL_INTERVAL=5s
INGESTION_REQUEST_TIMEOUT=120s
INGESTION_MAX_ATTEMPTS=5
```

Run `go run ./cmd/server`. The normal config → logger → PostgreSQL → Goose →
bootstrap → repositories/services → HTTP sequence is preserved. Workers start
after dependency and router initialization. Migration CLI commands start neither
workers nor storage, backfill or HTTP.

The URL and token have no credential fallback: an absent/blank URL or token, or
`INGESTION_ENABLED=false`, disables workers. HTTP auth, documents and queueing
continue to work. Supplying valid credentials later and restarting resumes queued
jobs. A configured but unreachable Python service never blocks API startup;
request failures are handled by the queue. A malformed configured URL or invalid
numeric/duration setting is an explicit configuration error, with secrets omitted.

Defaults are one worker, 5s polling, 120s request/operation timeout and five
attempts. Workers must be 1..8, attempts 1..100, polling 1ms..1h and timeout 1s..1h.
Keep one worker on the target 2 vCPU / 4 GB VPS unless measurements justify more.
Changing max attempts affects new/backfilled jobs and explicit retries; existing
jobs retain their stored budget.

`AI_SERVICE_URL` is operator-controlled and accepts only an HTTP(S) origin
(optional trailing slash), without credentials, query, fragment or path. Bind
Python to loopback or a private interface and restrict access using network rules;
do not expose it via a public reverse proxy. Use TLS on networks where documents
or the shared token could be intercepted. Go cannot prove a hostname's network
isolation. Redirects and ambient HTTP proxies are disabled to prevent forwarding
private documents or credentials elsewhere.

## Private request

```http
POST /internal/v1/ingest
Content-Type: multipart/form-data; boundary=...
Accept: application/json
X-Service-Token: <shared secret>
```

| Multipart field | Meaning |
| --- | --- |
| `document_id` | Parent document UUID |
| `document_version_id` | Exact version UUID; identity for this operation |
| `mime_type` | Validated original MIME type |
| `file` | Stream of original PDF, DOCX or TXT, with original filename |

Go streams the multipart body; Python must accept an unknown Content-Length /
chunked HTTP/1.1 upload. No storage key, disk path, callback URL or user-supplied
destination URL is sent. The original is not loaded entirely into Go memory.

## Success response

Return exactly HTTP 200, `Content-Type: application/json`, UTF-8:

```json
{
  "document_version_id": "7f6ffb5a-2621-490b-b9d3-6a3a15659c24",
  "chunks": [
    {
      "chunk_index": 0,
      "content": "Текст фрагмента документа",
      "page_number": 5,
      "section_number": "2.3.1",
      "section_title": "Порядок согласования"
    }
  ],
  "warnings": []
}
```

`page_number`, `section_number`, `section_title` may be null or absent. Use null
when the format has no meaningful page/section. Index and content are required
and cannot be null. Warnings may be omitted/null/empty; they are never logged or
exposed by the job endpoint because they may contain document text.

Go rejects unknown fields, malformed JSON, trailing JSON values, invalid UTF-8,
a mismatched version UUID, or any of the following limits:

| Value | Limit |
| --- | --- |
| Raw response body | 16 MiB, bounded even without Content-Length |
| Chunks | 1..2000 |
| Chunk index | Unique integer, 0..1,000,000; gaps allowed |
| Chunk content | Nonblank, no NUL, at most 64 KiB UTF-8 bytes each |
| Total chunk content | At most 8 MiB UTF-8 bytes |
| Page number | Null or integer 1..1,000,000 |
| Section number/title | Null or at most 128/512 UTF-8 bytes, no NUL |
| Warnings | At most 100 strings, each at most 1024 bytes, no NUL |

Python should use stable indexes and deterministic output for the same version.
Delivery is **at least once**: it may receive the same original more than once
after a timeout, restart or lease recovery. It must not publish versions or
write Go's database. A successful HTTP response is not a publication acknowledgement.

## Errors and retries

Network errors, timeout, HTTP 408/429/5xx are transient. Other HTTP failures,
redirects, wrong content type and invalid success payloads are permanent.
Response bodies and raw remote/network errors are never stored or logged.
Cancellation aborts the request and streaming producer, closing the source file.

Transient failures use 5s, 10s, 20s, 40s, 80s, 160s, then at most 5 minutes
between attempts. Actual execution can be delayed by polling/other queued work.
Attempts increment when claiming, including recovered crashed attempts. At the
stored maximum the job and version become failed. Originals, previous good
chunks and the active pointer remain intact on every unsuccessful attempt.

Safe `last_error` codes: `ai_unavailable`, `ai_timeout`, `ai_rejected`,
`invalid_ai_response`, `source_unavailable`, `lease_expired`, `processing_failed`.
Unknown internal codes are replaced with `processing_failed`.

## Transactions, concurrency and recovery

Migration [00005_ingestion.sql](../migrations/00005_ingestion.sql) adds jobs and
version-bound chunks; migrations 1–4 are unchanged. Each document/version insert
and its pending job commit together. Existing DocumentService cleanup removes a
new disk original if the transaction fails. Disk and PostgreSQL cannot share a
transaction; crash/ambiguous-commit reconciliation remains an operational task
as described in [versioning.md](versioning.md).

At each normal startup, `BackfillUploaded` inserts missing jobs for uploaded
versions with `ON CONFLICT DO NOTHING`. This runs even with workers disabled,
never resetting an existing job, retry budget or lease. No ready/failed version
is silently requeued.

Claims use row locks with `FOR UPDATE SKIP LOCKED`. Concurrent processes claim
different jobs without waiting for a busy row. A processing job carries a fresh
random lease token and a lease lasting operation timeout + 30s. No PostgreSQL
transaction spans a Python HTTP request. The default operation deadline is 120s;
there is no heartbeat. Increase timeout (maximum 1h) for slower documents.

Every completion/failure/retry checks processing status, current token and lease.
An expired worker cannot commit chunks, status or publication. Expired jobs are
reclaimed with a new token; a crashed final attempt is reaped as failed. Reaping
handles one exhausted job per claim invocation and continues on later polls.
Workers shut down with the application context, before storage/pool/logger close.
Unfinished jobs recover on lease expiry; shutdown never marks them completed.

Successful completion locks the parent document and performs chunk replacement,
version ready, conditional active-pointer update and job completed in **one
transaction**. A final lease check rolls back even if the lease expires during
chunk insertion. Insertion/validation failures preserve all previous chunks.
Automatic publication never moves the active pointer to a lower version number.
A late older result can become ready/completed without replacing a newer active
version. The first uploaded original remains downloadable before indexing,
preserving existing behavior; readiness is still required for future search.

`ChunkRepository.ReplaceVersionChunks` is a trusted maintenance operation;
workers exclusively use fenced `IngestionRepository.CompleteJob`. A unique
`(document_version_id, chunk_index)` B-tree also serves queries by version;
the GIN index uses `to_tsvector('russian'::regconfig, content)`. Search API and
retrieval authorization are outside this change. Future retrieval must check
document ACL and use only the active ready version.

## Admin API

Upload/version metadata now includes nullable `ingestion_job_id`. Newly uploaded
versions always have one; legacy uploaded versions receive it through backfill.
Existing document/ACL access rules still apply to version metadata.

```http
GET /api/v1/ingestion-jobs/{id}
Cookie: ai_session=...
```

```json
{
  "id": "fa894b11-bebd-42ae-bd4a-9cf8284b0863",
  "document_version_id": "7f6ffb5a-2621-490b-b9d3-6a3a15659c24",
  "status": "pending",
  "attempts": 1,
  "last_error": "ai_unavailable",
  "created_at": "2026-10-24T00:00:00Z",
  "completed_at": null
}
```

`last_error` is omitted when absent. Only admin can read jobs. No token, lease,
storage key, raw document text or remote error appears in JSON.

```http
POST /api/v1/ingestion-jobs/{id}/retry
Cookie: ai_session=...
Origin: <configured trusted origin>
X-CSRF-Token: <session CSRF token>
```

No body is required. Only failed jobs can be reset: the same job ID becomes
pending, attempts zero, current configured max attempts, immediately eligible;
the version becomes uploaded. Existing good chunks and active pointer remain.
Returns 200 with the job representation. Other states return 409, unknown/malformed
ID returns 404. Missing authentication returns 401; member, bad Origin or missing/
invalid CSRF returns 403. The service also checks admin independently of middleware.
There is no arbitrary command, callback or public Python endpoint.

## Python developer checklist

Implement only the authenticated private multipart endpoint above, streamed
upload handling, bounded PDF/DOCX/TXT extraction and version-bound chunk response.
Validate `X-Service-Token`, limit input/resources, handle cancellation and duplicate
delivery, return meaningful HTTP status codes, and keep original content and
credentials out of logs. Do not call a public LLM API for ingestion. Go owns all
retry and publication decisions.

## Verification

```powershell
go mod tidy
go test ./...
go vet ./...
go build ./cmd/server
$env:TEST_DATABASE_URL = 'postgres://test:password@127.0.0.1:5432/ingestion_test?sslmode=disable'
go test ./... -count=1
go test -race ./internal/aiclient ./internal/service ./internal/transport/http/... ./internal/repository/postgres ./tests/integration
```

Integration tests require an explicit disposable PostgreSQL database with `_test`
suffix; they never fall back to DATABASE_URL and create/drop only owned schemas.
Python is replaced by httptest only in tests. Coverage includes atomic enqueue,
backfill/uniqueness, concurrent claims, retries/exhaustion, expired and stale leases,
rollback, version binding, monotonic publication, API RBAC/CSRF, unavailable Python,
stream cancellation, graceful shutdown and entrypoint without AI credentials.
