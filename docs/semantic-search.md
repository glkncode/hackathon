# Semantic search pilot

## PostgreSQL 17 upgrade safety (before changing the image)

The verified main-local activation and rollback are recorded in
[semantic-search-main.md](semantic-search-main.md).

The current `postgres:17-alpine` uses musl; `pgvector/pgvector:pg17-bookworm`
uses glibc. Both use PostgreSQL major 17 and `/var/lib/postgresql/data`, but
collations must be verified. Never remove `postgres_data` or run `down -v`.

1. Record `docker compose ps`, the postgres volume mount and `SHOW server_version`,
   `SHOW data_directory`, database encodings/locales and collation versions.
2. Stop API writes (`docker compose stop api`). Create a custom-format `pg_dump`
   and `pg_dumpall --globals-only` as files inside the original container, then
   `docker cp` them to a private ignored backup directory. Archive the separate
   document volume read-only, record SHA256 and verify every storage key exists.
   Never redirect a binary dump through PowerShell's text stdout.
3. Initialize a **new named volume** with PostgreSQL 17 + pgvector. Restore roles
   and the logical archive with `pg_restore --create --exit-on-error`. Verify the
   archive listing and an actual restore: table counts, deterministic row hashes,
   roles, locales and extracted original-file hashes must match the source.
   Never mount Alpine PGDATA directly in the Debian image.
4. Apply migration 00008 to the restored target. Verify vector(384), original data
   fingerprints and migration status before selecting the new volume using
   `POSTGRES_DATA_VOLUME`. Keep the original named volume and image for rollback.
5. Start the API only after this verification. Migration 00008 checks extension
   availability with an actionable error before creating tables. Other services
   can then start normally. Rollback retains the original volume and backup.

Integration tests require a disposable pgvector-enabled PostgreSQL **17** database
ending in `_test`. No production DB cleanup is part of this procedure.

## Implementation and identity

Feature flag `SEMANTIC_SEARCH_ENABLED=false` is the default. FTS and Query
Understanding keep their existing behavior. With indexing enabled, one Go CPU
index worker reconciles all active-ready versions every five seconds, including
documents uploaded before activation. It does not change ingestion readiness.
There is no additional Python service, message broker, GPU or local Qwen.

`SEMANTIC_INDEXING_ENABLED` controls the worker independently. When unset it
follows `SEMANTIC_SEARCH_ENABLED` for compatibility. Set indexing true with search
false for backfill, verify all active-ready versions, then enable search.

`app/embeddings/onnx.py` promotes the tested experimental E5 implementation.
The batch adapter uses its tokenizer, prefixes and token windows and is tested
numerically against its original single-window inference. The lazy singleton
checks the pinned model and tokenizer SHA256 before loading one CPU ONNX session
(2 intra-op / 1 inter-op threads). Missing/corrupt files return 503; `/health`,
ingestion and RAG continue working. Nothing downloads on startup or build.

Migration **00008_semantic_embeddings.sql** adds:

- `semantic_indexes`: version, model, immutable HF revision, dimensions, formatting
  revision, readiness, attempts, retry deadline, fenced lease, safe error code,
  timestamps. Identity is `(version, model, revision, format_revision)`.
- `semantic_chunks`: atomic per-source progress and expected window count/tokens.
- `embedding_windows`: original chunk/version, window ordinal/token span,
  `public.vector(384)`, timestamp. Composite foreign keys prevent cross-version
  source associations. Cosine score per source is **max over all its windows**.

Identity is `intfloat/multilingual-e5-small`, revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3`, dimension 384, formatting revision
`e5-exact-prefix-v1-title-content-512-overlap64-mean-l2-max`. The hardcoded model
SHA256 is `ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665`;
tokenizer SHA256 is `0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39`.
Hashes are enforced by Python; the DB identity selects this pinned implementation.
A change to model artifacts or formatting requires a new identity/migration,
not an in-place replacement of the weights under the same identifier.

Passages contain title + newline + original content. Prefixes are `query: ` and
`passage: `. Model context is 512 tokens including special tokens; overlap is
64 content tokens. Long queries are rejected, long passages retain every window.
Original document chunks and historical versions are never rewritten by indexing.

Each chunk manifest and its windows commit in one transaction. `FOR UPDATE SKIP
LOCKED` claims jobs; every write checks a nonexpired, random lease token. Saved
chunks survive retry/restart; only missing chunks are requested again. A version
becomes ready only when all original chunks have their expected windows. Backoff
is exponential, capped at five minutes; transient outages retry indefinitely.
Attempts/errors and progress are available through `EmbeddingRepository.State`
and SQL, independently of the document status. Operations have bounded contexts.

`EmbeddingRepository.Reindex` atomically resets only the configured vector index,
clears its derived vectors/manifests and invalidates old leases. FTS remains ready.
It preserves original chunks, old document versions, chat provenance, and indexes
of other model identities. During this same-identity rebuild retrieval uses FTS;
this pilot does not implement a zero-downtime model-switching administration API.

## Private API

Both endpoints use the existing middleware: exactly one `X-Service-Token`, checked
before parsing the body. Documents, questions, raw LLM replies and secrets are
never logged by these modules.

`POST /internal/v1/embeddings`:

```json
{"kind":"query","inputs":["Вопрос пользователя"]}
```

```json
{
  "model":"intfloat/multilingual-e5-small",
  "revision":"614241f622f53c4eeff9890bdc4f31cfecc418b3",
  "dimensions":384,
  "format_revision":"e5-exact-prefix-v1-title-content-512-overlap64-mean-l2-max",
  "embeddings":[["384 finite normalized numeric values per window"]],
  "windows":[{"input_index":0,"window_index":0,"start_token":0,"end_token":10,"total_content_tokens":10}]
}
```

The illustrative vector above is abbreviated; actual elements are numbers.
Unlike a truncating one-vector-per-text contract, long passages return multiple
vectors, ordered by input then window. `windows[n]` maps `embeddings[n]` to the
input's real chunk ID in Go. Query requests have exactly one window per input.

Bounds: 1–8 inputs, 256 KiB combined UTF-8 text, 300 KiB request body, 32768 total
window tokens and at most 128 windows. ONNX activation batches contain at most
8 x 512 tokens. One CPU request at a time; excess concurrency gets 503, without
an unbounded queue. An HTTP timeout retains its CPU slot until inference actually
finishes. Default Python/Go request timeout is 30 seconds; body responses in Go
are bounded at 2 MiB. Invalid vectors, norms, model/format revisions, cardinality,
window order, missing tails, content types and HTTP responses are rejected.

`POST /internal/v1/evidence` accepts the existing private RAG request projection
(`query`, empty `history`, bounded authorized `sources`). Qwen selects only direct
support and returns `{"evidence":[{"chunk_id":"UUID","quote":"source quote"}]}`.
Both Python and Go validate IDs, uniqueness and the quote against that source's
content. Only Unicode whitespace is collapsed for quotation matching (PDF NBSP,
tabs and line breaks); no words, negations, punctuation or numbers are changed.
Empty evidence means abstention. Invalid/unavailable evidence causes FTS fallback.
This is a conservative LLM gate, not a formal entailment proof.

## SQL authorization and retrieval

The vector repository materializes allowed chunks **before** the exact cosine
scan and LIMIT. SQL checks current `users.is_active`, agreement with the trusted
role, admin or actual `document_access`, active document version and `ready`
status, and compatible ready semantic identity. It uses `public.<=>`, groups
windows by real chunk ID, and returns the existing citation DTO without storage
keys. There is no global top-k followed by ACL filtering in Go or ANN index.
Distance ordering uses exact cosine. The public `score` projection clamps cosine
to [0,1] solely because existing chat citation storage requires nonnegative
scores; this does not filter candidates or act as an evidence threshold.

Coverage and vector search share a repeatable-read snapshot. If any version
visible to this actor lacks a compatible ready index, the entire request falls
back to FTS. This conservative pilot policy favors complete coverage over partial
semantic availability. Visibility is rechecked in SQL before the evidence call
and again after it, before returning sources to Chat/Search. Existing chat save
checks and historical ACL redaction remain in effect.

`SEMANTIC_SUPPLEMENT_FTS=true` appends unique FTS candidates to the semantic pool,
then applies the same evidence gate and source limit. Semantic order is retained;
no mandatory RRF or score cutoff is introduced. The default is semantic-only.

Timeout, unavailable/corrupt model, invalid/incompatible response, unfinished
index, failed vector SQL, or failed evidence check cause the existing FTS path
to run with the original caller context. Logs contain a fixed technical reason,
not input text. Caller cancellation propagates instead of starting more work.

## Local commands

After completing the PostgreSQL volume compatibility procedure above:

```powershell
docker compose build
# Explicit one-time download into the persistent embedding_models volume:
docker compose run --rm --no-deps --user root ai-service python -m scripts.download_embeddings
# In .env: SEMANTIC_INDEXING_ENABLED=true, SEMANTIC_SEARCH_ENABLED=false.
docker compose up -d --build
# Verify complete indexing for all active-ready versions, then set search true
# and recreate only api: docker compose up -d --no-deps --no-build api
```

Keep `SEMANTIC_SEARCH_ENABLED=false` to retain FTS-only behavior. Rebuilds reuse
the named model volume. Qwen continues using the existing LAN configuration.
The model download command is optional if verified pinned artifacts have already
been copied into `/models/e5-small`; runtime model loading never uses the network.

The original application and DB need not be interrupted to reproduce the pilot:

```powershell
docker compose build api ai-service
docker pull pgvector/pgvector:pg17-bookworm
# Put verified pinned artifacts in .local-integration/e5-small first.
./scripts/semantic-pilot.ps1
# After code changes, recreate only the stateless pilot services:
./scripts/semantic-pilot.ps1 -RecreateServices
```

Pilot resources: PostgreSQL 1 GiB, Python 4 GiB / 4 CPU, each Go service 512 MiB.
Docker Desktop was measured at **16691892224 bytes RAM / 12 CPU**, not assumed to
have all 32 GiB of host RAM. Loopback ports 18091/18092/18093 compare FTS/semantic/
semantic+FTS and 18094 exposes private AI for testing. The original React address
is unchanged. `./scripts/semantic-pilot.ps1 -Action stop` stops only pilot
containers and retains both named DB/document volumes.

Evaluation scripts, frozen synthetic corpora, source mappings, answers and
reviewed metrics are under `experiments/semantic-pilot/`; see its README and
`docs/semantic-search-verification.md`. Existing experiment results are retained.
