# Semantic pilot: Go + pgvector + CPU E5 + existing Qwen

Исторические `results/2026-10-24*` перенесены во внешний архив, проверенный
по SHA-256: [отчёт об уборке](../../docs/repository-cleanup-report.md).
Для проверки старой разметки сначала восстановите нужный каталог из архива.
Команда `export` может вновь создать локальную копию результатов; runtime их не читает.

The original `experiments/rag` results remain unchanged. The new implementation
is tested through the real Go Search/Chat endpoints and ingestion workers.
No fixture/question-specific rules are installed in application code.

Two synthetic datasets were frozen before their respective observations:

- `corpus.json`: initial independent 4 documents, 12 positive / 6 negative
  questions. Its first result is retained under `results/2026-10-24`.
- `corpus-final.json`: another 4 previously unqueried documents, 12 positive /
  6 negative questions, after the general Unicode-whitespace quotation fix.
  Its result is under `results/2026-10-24-final`.

Both include the old rag corpus as regression/distractor documents. The first new
set becomes a diagnostic set in the final run. Final evaluation contains 56
visible chunks, 18 active documents including a denied document, plus a genuine
historical travel version. Gold mapping is frozen from actual PostgreSQL chunk
IDs before retrieval; exported manifests include the exact source text.

The initial run exposed a quotation-protocol problem: PDF extraction preserves
NBSP and line breaks, while Qwen can return ordinary spaces. The fix collapses
only whitespace in Python and Go; words, negations, numbers and punctuation must
still match. No cosine threshold, synonym list, planner, RRF or per-question
rule was adjusted. Initial 75% gated recall is retained rather than overwritten.

The final unseen set measured 100% raw Recall@5/10 and 91.7% gated Recall@5/10;
11/12 grounded answers, correct citations for all generated answers, and 0/6
false factual negative answers in each semantic arm. FTS missed these paraphrased
questions. Results are small-sample pilot evidence; they are not a general
production guarantee. The remaining `f03` abstention is preserved.

## Reproduce on Windows

Provision the verified model in `.local-integration/e5-small` and use a Python
environment with `experiments/rag/requirements.txt`, except PyMuPDF can be `<2`.
No benchmark downloads weights. `evaluate.py` reuses the old tested document
fixtures and gold mapper; PDF generation uses `C:/Windows/Fonts/arial.ttf`.

```powershell
docker compose build api ai-service
docker pull pgvector/pgvector:pg17-bookworm
./scripts/semantic-pilot.ps1
$py = '.local-integration/semantic-venv/Scripts/python.exe'
& $py experiments/semantic-pilot/evaluate.py prepare
& $py experiments/semantic-pilot/evaluate.py compare
& $py experiments/semantic-pilot/evaluate.py answers
& $py experiments/semantic-pilot/evaluate.py verify
& $py experiments/semantic-pilot/evaluate.py export

# Separate fresh set, retaining the initial observations:
& $py experiments/semantic-pilot/evaluate.py prepare --final
& $py experiments/semantic-pilot/evaluate.py compare --final
& $py experiments/semantic-pilot/evaluate.py answers --final
& $py experiments/semantic-pilot/evaluate.py verify --final
& $py experiments/semantic-pilot/evaluate.py export --final
# Run only after quality evaluation: this adds an extra long source.
& $py experiments/semantic-pilot/evaluate.py probe --final
./scripts/semantic-pilot.ps1 -Action stop
```

`prepare` refuses an existing frozen manifest. Successful retrieval/answer
observations are resumed without rerunning them. Corpus membership and UUIDs
are verified, not assumed from question words. Don't remove frozen outputs to
retune results; use a new corpus, DB and result directory for another evaluation.
Final preparation intentionally reuses the initial isolated DB/member/document
state and adds only the final new documents. It checks all active versions,
including ones not yet enqueued, before declaring indexing complete.

`verify` revokes/regrants only a synthetic document ACL, checks historical chat
redaction, stops only the private pilot Python service, checks FTS/Search/Chat
fallback, and restarts only the named pilot DB/Go service. It never removes a
volume or modifies the original application. `probe` uploaded a 2154-token source
and verified 5 persisted windows, original source identity, unit norms, overlapping
coverage including its tail, and retrieval of the tail through real Go Search.

Review is explicit and hash-bound. `review.json` records an agent's claim-by-claim
assessment of all 54 final answers, including missed positives and abstentions;
this is not an independent human expert review. To validate it:

```powershell
& $py experiments/semantic-pilot/review_results.py experiments/semantic-pilot/results/2026-10-24-final
```

For a new run, write a fresh review tied to its actual `answers.json` SHA256.
Never copy verdicts to changed answers. Status `generated`, citation membership
or substring checks alone do not establish factual grounding.

## Measurement boundaries

Public `semantic` and `hybrid` search metrics include the evidence gate. `hybrid`
means append unique FTS candidates while retaining semantic order; it is not RRF.
The raw diagnostic arm uses the private E5 endpoint and exact ACL-filtered SQL.
Its timing includes Windows `docker exec psql` overhead. Raw hybrid timing is an
additive diagnostic estimate; it is not a measured public endpoint latency.

Recall is macro over positive questions and all required gold chunks; precision
is micro over minimal supporting gold chunks including negative queries. Empty
FTS precision is undefined, not 100%. Every negative raw top-k returned candidates;
the public gate rejected the six final negatives.

Latencies are one warm observation per question, fixed FTS/semantic/hybrid order,
with a small sample. Qwen prefix caching and network state can influence arm
differences; no hybrid speed advantage is claimed. Resource files contain sampled
Docker working memory and CPU (100% = one core), not exact RSS peaks. PostgreSQL
was also used by isolated-schema integration tests during part of the recording.
The remote Qwen server's hardware usage was not measured.

Indexing files report queue-to-ready wall time, including polling/retry/model
startup and contention between two Go pilot arms. They are not pure ONNX
throughput numbers. Docker Desktop memory/CPU and immutable container image IDs
are in `environment.json`; no service tokens or Qwen credentials are exported.
