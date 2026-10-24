# Private RAG generation: Go -> Python

Python provides generation only. Go owns retrieval, ACL, version selection,
chat history persistence and rendering citations from source metadata.
Python does not access PostgreSQL, search documents or call tools.

## Request

```http
POST /internal/v1/rag/generate
Content-Type: application/json
X-Service-Token: <same AI_SERVICE_TOKEN as ingestion>
```

```json
{
  "query": "Как согласовать служебную записку?",
  "history": [{"role": "user", "content": "Я готовлю служебную записку."}],
  "sources": [{
    "chunk_id": "7f6ffb5a-2621-490b-b9d3-6a3a15659c24",
    "document_title": "Инструкция по документообороту",
    "version_number": 2,
    "section_number": "4.6",
    "page_number": 13,
    "content": "Служебную записку согласует руководитель подразделения."
  }]
}
```

Only Go-authorized sources may be sent. History roles are user/assistant only.
Unknown fields (including user-supplied inference URLs) are rejected.
Source chunk IDs must be unique, nonzero UUIDs. Optional section/page fields
default to null. Query is required and nonblank; history defaults to [].

Limits: query/history messages 4000 characters each, 16 history messages,
32 sources, source content 16000 characters each, title 512 characters,
section number 128 characters, page 1..1000000, version integer >= 1.
The combined validated request serialized as JSON with ensure_ascii=False
must fit 64 KiB of UTF-8. The raw HTTP body limit is 1 MiB, also for chunked
uploads. Input is rejected, never silently truncated.

## HTTP 200

Exactly these response fields:

```json
{
  "status": "answered",
  "answer": "Согласуйте служебную записку с руководителем подразделения.",
  "used_source_ids": ["7f6ffb5a-2621-490b-b9d3-6a3a15659c24"],
  "clarification_question": null
}
```

answered requires a nonblank answer and at least one unique source ID from the
current request. needs_clarification returns answer=null, used_source_ids=[]
and a nonblank clarification_question. insufficient_information returns both text
fields null and used_source_ids=[]. With sources=[] this state is returned
without an inference call, even if no model is configured.

Status selection distinguishes missing values from missing evidence:

- `answered`: sources support the requested facts, a useful partial answer, or
  an explicitly unfilled field. Describe known roles/requirements and identify
  the missing values without inventing people, dates, numbers or assignments.
- `needs_clarification`: the subject or materially different interpretations of
  the question cannot be resolved from the question and history. An unfilled
  template field alone does not require clarification.
- `insufficient_information`: the question is clear, but sources provide neither
  the requested facts nor evidence of the corresponding blank field. Silence
  about a requirement does not prove that the requirement does not exist.

For tables split across sources, the prompt instructs the model to interpret
the introduction, headers and rows together only when their relationship is
supported, and cite the actual rows supporting its claims. Placeholders and
filling notes are not concrete values; a signature does not establish a control
assignment. These are general model instructions, not answer substitutions.
Python validation preserves a valid model status; invalid states/citations
produce 502 rather than being converted to insufficient_information.

Go should render authoritative titles, sections, page numbers and links using its
own source metadata, not parse them from answer prose. The model is instructed not
to include those citation details in prose. ID validation is enforced in code;
semantic grounding and instruction-following cannot be guaranteed by ID checks
or a system prompt. Do not treat these checks as proof of correctness.

## Errors and Go fallback

RAG failures contain no document/query/provider response text:

```json
{"error": {"code": "llm_unavailable"}}
```

| HTTP | Code | Go behavior |
| --- | --- | --- |
| 503 | llm_unavailable | Model absent, network failure, inference 429/5xx; offer search with citations |
| 504 | llm_timeout | End-to-end inference deadline; offer search with citations |
| 502 | llm_upstream_error | Inference rejected request, including redirects; offer search with citations |
| 502 | llm_invalid_response | Invalid/truncated JSON, invalid state or unknown citation; offer search with citations |
| 422 | invalid_rag_request | Invalid input/context limit; fix request, do not retry unchanged |
| 401 | detail: Invalid service token | Existing shared authentication error |
| 413 | detail: Request exceeds upload limit | Raw request too large |

No response is silently converted to a successful answer after inference failure.
No automatic inference retries. No question/document content is logged by this
service. Configure any reverse proxy or inference server separately to avoid
request-body logging.

## Inference configuration

The adapter supports the chat-completions protocol over HTTP, including Ollama:

```dotenv
LLM_BASE_URL=http://127.0.0.1:11434/v1
LLM_MODEL=<already-installed-and-approved-model>
LLM_API_KEY=
LLM_TIMEOUT=60
```

The final endpoint is LLM_BASE_URL + /chat/completions. The server must support
response_format=json_object and return choices[0].message.content as a JSON string,
with finish_reason=stop. No native Ollama /api/chat adapter is needed when using
its /v1 compatibility endpoint. No models are downloaded by the application.
Unset base/model is supported: ingestion remains available, generation returns 503.
Timeout is finite, >0 and <=300 seconds and includes waiting for an inference
slot, HTTP I/O and reading the response. At most four calls run per Python process.
Provider response size is limited to 128 KiB; max_tokens is 4096.
External proxies and redirects are disabled.

Loopback addresses, localhost, host.docker.internal and the Docker hostname
ollama are allowed by default. Other hosts require LLM_ALLOW_REMOTE=true in
addition to explicit team/organizer approval; do not set it for an unapproved cloud
provider. Only the operator controls the endpoint. URL credentials, query strings
and fragments are rejected. Invalid configuration prevents startup; unavailable
inference does not.

## Local check

From ai-service, with the existing .venv and dependencies installed:

```powershell
$env:AI_SERVICE_TOKEN = "<same-private-token-as-Go>"
$env:LLM_BASE_URL = "http://127.0.0.1:11434/v1"
$env:LLM_MODEL = "<approved-installed-model>"
$env:LLM_TIMEOUT = "60"
.\.venv\Scripts\python.exe -m uvicorn main:app --host 127.0.0.1 --port 8000
```

Without a model, leave LLM_BASE_URL and LLM_MODEL unset and verify 503 for the
request above, and 200 insufficient_information for sources=[].
The service does not automatically read .env.

Swagger: http://127.0.0.1:8000/docs -> POST /internal/v1/rag/generate.
Enter the service token and paste the JSON request above.

Example using a local JSON request file:

```powershell
curl.exe http://127.0.0.1:8000/internal/v1/rag/generate -X POST -H "X-Service-Token: $env:AI_SERVICE_TOKEN" -H "Content-Type: application/json" --data-binary "@rag-request.json"
```

## Verification and quality evaluation

```powershell
.\.venv\Scripts\python.exe -m pytest -q
```

tests/test_rag.py uses a real loopback HTTP mock inference server. It checks
the adapter's HTTP path and protocol plus success, clarification, insufficient
information, absent sources, unknown/duplicate IDs, invalid JSON/states, timeout,
unavailability, token validation, request/context/response limits and prompt
injection handling. No actual LLM or external inference service is needed.
The injection test proves separation into system rules vs untrusted data and
rejection of unknown IDs; it does not establish that a real model resists attacks.

Before selecting a production model, evaluate a versioned set of real questions:
answerable, ambiguous, missing evidence, conflicting clauses, multi-turn questions,
malicious source text and adversarial document titles. Have reviewers check each
claim against cited chunks, citation precision/recall, abstention quality,
clarification usefulness and leakage/injection resistance. Include plausible
wrong answers citing valid IDs: ID validation alone cannot detect them.

`tests/test_rag_templates.py` uses mock LLM outputs to check preservation of
valid partial answers and statuses, complete table context, untrusted-data
boundaries, privacy and unchanged citation/state validation. These tests verify
the Python contract, not the real model's semantic behavior.

For an opt-in local Qwen evaluation, prepare a private JSON array of existing
source rows with `chunk_index` plus the request source fields. The benchmark
uses the unchanged working-group DOCX indices from the ingestion golden fixture;
use IDs from the actual existing version, not synthetic IDs. It performs no
retrieval or database writes. From the repository root:

```powershell
& ai-service/.venv/Scripts/python.exe ai-service/scripts/evaluate_template_rag.py --sources .local-integration/rag-template-eval/sources.json --output .local-integration/rag-template-eval/accepted.json --env-file .env
```

This explicitly loads only `LLM_*` configuration from the supplied env file,
requires `qwen3.6-35b-a3b`, and repeats each of 12 cases at least five times with
the production generation parameters. `--core-only` selects the original five
questions; `--case` selects a particular case and `--prompt-file` can load a
saved baseline `prompts.py` for comparison.
Each raw response is replayed through production validation without a second
inference to detect status conversion. Console output contains only case labels,
status/check counts and preservation flags. The report contains raw answers;
keep it and the source export private, under the ignored `.local-integration/`
directory, and review claims against cited rows. Lexical benchmark checks alone
do not establish semantic correctness or detect every invented name/value.

See [the measured Qwen before/after results](rag-template-generation-verification.md).
