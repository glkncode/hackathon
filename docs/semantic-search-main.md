# Main local activation — 24 October 2026

Semantic Search is **enabled in the main app**, http://localhost:8080.
All four main Compose services are healthy. This report describes the actual
main database and frontend; pilot measurements are not substituted for these tests.

## Backup and verified migration

Private backup directory: `D:\Projects\hackathon\.local-integration\main-semantic-20261024`.
It is ignored by Git. Files include real user data and private runtime settings;
do not publish the directory or its image archive.

| Artifact | Purpose / verification |
|---|---|
| `database.dump` | PG17 custom-format logical dump, written inside original container then copied with docker cp |
| `globals.private.sql` | Roles and global objects, including original app role attributes |
| `archive-list.txt` | Successful pg_restore --list |
| `documents.tar.gz`, `document-files.sha256` | Read-only archive of original document volume; extracted into separate volume and SHA256 checked |
| `baseline-tables.json`, `restored-tables.json` | Exact equal counts and sorted-row fingerprints for all 12 original tables |
| `baseline-database.json`, `docker-metadata.json` | Original PG17.11 Alpine, locale, roles, image IDs and mounts |
| `*-private.json`, `original.env.private`, `original-compose.private.yml` | Private pre-cutover runtime configuration |
| `post-migration-verification.json`, `main-before-activation.json` | Original data fingerprints unchanged after migration and main startup; full backfill before search enabled |
| `rollback-images.tar`, `rollback.ps1`, `rollback-rehearsal.json` | Saved rollback runtime and real rehearsal on separate Alpine PG17 volume |
| `backup-sha256.json`, `final-artifact-sha256.json` | Archive checksums and final artifact integrity manifest |

Before migration the original database contained 1 user, 1 document/version,
36 chunks, 0 ACL grants, 158 messages, 50 chats, 41 source records,
64 dependency records, 1 ingestion job and 9 sessions. Migration version was 7.
API writers remained stopped throughout dump, restore and comparison.

An actual `pg_restore --create --exit-on-error` into a **new** PG17 pgvector volume
restored every original table with equal counts and fingerprints. Roles were
restored; app remained the login/superuser role used by the original installation.
Database locale stayed `en_US.utf8`; logical restore rebuilt indexes under glibc
(collation version 2.36). No Alpine PGDATA was mounted in Debian.

Migration 00008 was applied to the restored target before cutover. All original
table fingerprints except the migration bookkeeping remained equal. The initial
main startup also retained those fingerprints and indexed 36/36 chunks while
`SEMANTIC_SEARCH_ENABLED=false`, `SEMANTIC_INDEXING_ENABLED=true`.
The pinned model hashes and real private embedding API were verified before
activation. Main search was enabled only after these checks and full readiness.

## Images, volumes and current state

PostgreSQL: `pgvector/pgvector:pg17-bookworm`, PostgreSQL **17.11**, vector **0.8.7**.
Local image ID: `sha256:ac08538c6f8b9904c33c8224c5e5706dbe760aca29db1d096972b4052c22a75d`.
Go image ID: `sha256:563320b322f4aa1c4058a6bb957b66276364654a6fbe6c997429894021b5f28b`.
Python image ID: `sha256:dc98471b1aacb1ea5f1469bf67a9b9a0c50ab53e8c259d2b1e2f4b60f331a404`.
The existing frontend container was retained.

| Named volume | Use |
|---|---|
| `hackathon_postgres_semantic_20261024` | Main logically restored PG17 + pgvector |
| `hackathon_postgres_data` | Preserved original Alpine PG17 database; stopped before cutover, not mounted by the new service |
| `hackathon_documents_data` | Main original files, retained; fixture uploads added through normal API |
| `hackathon_embedding_models` | Persistent pinned E5 model at `/models/e5-small` |
| `hackathon_documents_restore_check` | Original document archive extraction/hash verification |
| `hackathon_rollback_rehearsal_pg` | Separate restore used only for rollback rehearsal, now stopped |

Model: `intfloat/multilingual-e5-small`, revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3`, 384 dimensions.
Model SHA256 `ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665`;
tokenizer SHA256 `0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39`.
Existing verified weights were copied into the persistent volume. No build or
startup downloads were introduced. Existing LAN Qwen configuration was preserved.

After synthetic E2E uploads and a v2 update: **2 users, 8 documents, 9 versions,
57 chunks and 57 embedding windows**. All **9 indexes ready**, including the
retained synthetic v1; **8/8 active-ready versions** covered. Zero duplicate
window identities and zero invalid dimensions/norms. Original DOCX still has
36 chunks and its downloaded bytes match the pre-migration SHA256.
Migrations 00001–00008 applied. Existing messages and original chunks were not
rewritten. The new fixture member, seven fixture documents and E2E chats remain
available for inspection; these are additions to the original data.

## Actual main Go → Python → pgvector → Qwen evaluation

Frozen corpus and executable runner: `experiments/semantic-main/`.
Same 12 independent positive questions and six negatives were submitted with
the flag off and on through the normal nginx/Go API at localhost:8080.
The corpus was fixed before retrieval; no query rules, score cutoffs or gate
adjustments were added. All eight documents were simultaneously accessible to
the fixture member through document ACL.

| Main measurement | Search off / FTS | Search on / semantic |
|---|---:|---:|
| Correct source in Search@10 | 0/12 | 12/12 |
| Fully correct grounded Chat answers | 0/12 | 12/12 |
| Correct citations on generated independent answers | no generated answers | 12/12 |
| Fabricated answers on six unanswerable questions | 0/6 | 0/6 |
| Search p50 / p95 | 6.34 / 12.33 ms | 2065 / 2619 ms |
| Chat p50 / p95 | 18.37 / 33.70 ms | 4098 / 4734 ms |

Correctness was explicitly checked claim by claim by the Codex agent against
the original source text; the review records input SHA256. This is not an
independent human expert assessment. The latency sample contains 21 questions
per arm (also two original-DOCX questions and the separate f03 regression),
fixed order off then on, with network/caching effects from remote Qwen. FTS
abstention is faster; these small local results do not establish production SLOs.

Semantic peak sampled memory: Python **912 MiB**, Go **45.91 MiB**, PostgreSQL
**51.14 MiB**. Maximum sampled CPU respectively **38.29%**, **1.06%**, **7.38%**
(Docker CPU percentage; 100% is one core). 21 samples in the semantic arm;
only one sample in the short FTS arm, insufficient for a CPU comparison.
Docker Desktop had 15.55 GiB / 12 CPU, within the 32 GB host.

Both original working-group questions succeeded with semantic Search and Chat.
The unchanged **f03 false negative remains**: exact raw ACL-filtered cosine
retrieval finds its supporting paragraph, while the evidence gate returns no
sources. `known-false-negative.json` records actual main results. It is excluded
from the independent 12-question metric. The gate was not weakened.

## Main E2E and React

Normal React Chat was operated in the browser at localhost:8080; it uses the
existing `/api/v1/chat/sessions/.../messages` flow through Go. Observed responses:

- Original DOCX: secretary records meeting results and stores materials;
  source card correctly shows version 1, paragraph 4.
- New DOCX table: medium pot → **260 ml**; contextual “А для большого?” → **430 ml**,
  both with the correct table row in the source card.
- Absent frequency of watering → explicit “Информация не найдена”.
- New PDF: torn parcel packaging → photograph damage and draw up an act with
  the courier; correct source, version and page 1 shown.
- Knowledge base displayed all eight documents ready, including original DOCX,
  three new DOCX and four PDF files.

Real screenshots: `e2e/react-original.png`, `react-table.png`,
`react-follow-up.png`, `react-no-answer.png`, `react-pdf.png` in the private folder.
Normal source-download URLs were checked for all eight documents; SHA256 matched
uploaded originals or the original pre-migration file.

Additional main API checks passed:

- ACL revoked for the fixture member: document disappears from Search, previous
  answer is redacted in history, version download returns 404. Grant restored.
- Anonymous access returns 401, invalid CSRF returns 403; session-cookie flow retained.
- Replaying the same Chat client message ID returns the identical response and
  leaves exactly two messages in that test session.
- New DOCX version changes the return time to 21:05. After ingestion/indexing,
  Search uses v2 and excludes v1; original v1 chunks remain.
- With the main Python container stopped, semantic Search falls back to FTS;
  Chat returns `mode=search` with real FTS sources. Python is running again.
- After restarting main PostgreSQL, Python and Go, all 57 vector rows have the
  same sorted-row fingerprint `c6625733f47aab1c14fc364716eaa1bf` and 8/8 active
  indexes remain ready. Original DOCX semantic retrieval also succeeds afterward.
- `go test ./...` and `go vet ./...` passed. Integration tests needing optional
  external environment are not claimed as newly executed database coverage;
  the main runner provides the actual DB/API checks above.

## Rollback

For immediate retrieval rollback while retaining all post-cutover data, set
`SEMANTIC_SEARCH_ENABLED=false` in `.env` and run:

```powershell
docker compose up -d --no-deps --no-build api
```

For full database rollback, stop writers and run the private prepared script:

```powershell
& 'D:\Projects\hackathon\.local-integration\main-semantic-20261024\rollback.ps1'
```

It checks all required images and original volumes before stopping main services,
recreates containers with saved private environment, original PG17-Alpine image
and **original `hackathon_postgres_data`**, retains the documents volume and
leaves the semantic database/model volumes intact. Post-cutover writes remain
in the semantic volume but are absent from the frozen original database. After
this full rollback use `docker start`/the rollback script to manage these restored
containers; plain current `docker compose up` would select the semantic setup.

Docker no longer resolved the old stateless API/AI image metadata by its saved
identifier. To make rollback executable, the exact old Go binary was extracted
from a stopped container with the same original image ID and packaged in
`hackathon-rollback-api:20261024`. The frontend root filesystem was snapshotted;
the current Python image is pinned as the compatible legacy-endpoint runtime
`hackathon-rollback-ai-compatible:20261024`. PG17-Alpine is tagged
`hackathon-rollback-postgres:20261024`. These four images are saved in
`rollback-images.tar`; restore missing local tags with `docker image load --input`
on that archive before running the script.

An actual rollback rehearsal restored the original dump into a **separate**
Alpine PG17 volume, matched all 12 original tables, then ran the original Go
binary against schema 7 and the compatible Python service. Health, login,
document listing, FTS and Chat succeeded. The rehearsal containers are stopped;
the original volume was not used for this rehearsal. The full main rollback
itself was not executed, since the main activation passed.

No database/volume deletion, PG18 upgrade, commit, push, VPS/CI changes or
deployment was performed.

## Files changed for main activation

- `internal/config/config.go`, `internal/config/semantic_test.go`: independent
  indexing switch, validation and compatibility/default tests.
- `cmd/server/main.go`: create AI client/start worker for indexing independently
  of search; public APIs and DTOs retained.
- `docker-compose.yml`: indexing environment and required explicit PostgreSQL volume
  name; existing .env files cannot silently reuse Alpine PGDATA with Debian.
- `.env.example`, private `.env`: switch guidance / actual verified main selection.
- `docs/semantic-search.md`, this report: safe logical restore/backfill sequence.
- `experiments/semantic-main/{corpus.json,evaluate.py,README.md}`: actual main tests.
- Ignored backup, verification, rollback tools and artifacts under the private
  directory above. Earlier pilot and unrelated dirty-worktree files preserved.
