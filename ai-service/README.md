# Python document ingestion

Implements the private API defined in [the Go contract](../docs/ingestion-contract.md).
Go stores originals, retries requests and saves returned chunks. Python uses isolated
temporary directories and removes them after parsing, including on errors.
Ingestion does not use embeddings or inference. The separate RAG generation
endpoint uses an explicitly configured inference API; see below.

## Local startup (PowerShell)

Run from the ai-service directory:

```powershell
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
$env:AI_SERVICE_TOKEN = "your-shared-private-secret"
.\.venv\Scripts\python.exe -m uvicorn main:app --host 127.0.0.1 --port 8000
```

Set the same AI_SERVICE_TOKEN in Go. Environment variables are not automatically
loaded from .env. Missing/blank tokens prevent Python startup.

For local testing, a private token is saved in ai-service/.env (ignored by Git).
To explicitly load that file when starting:

```powershell
.\.venv\Scripts\python.exe -c "from dotenv import load_dotenv; load_dotenv('.env'); import uvicorn; uvicorn.run('main:app', host='127.0.0.1', port=8000)"
```

Open http://127.0.0.1:8000/docs and send POST /internal/v1/ingest with a
X-Service-Token header, document_id and document_version_id (nonzero UUIDs),
mime_type and file. Swagger offers the token in the request headers through its
generated schema. PDF, DOCX and UTF-8 TXT are supported.

The response contains only document_version_id, chunks and warnings.
PDF chunks retain 1-based page numbers. DOCX/TXT page numbers are null.
Numbered heading detection is heuristic; section fields remain null until a heading
is found. DOCX paragraphs and tables retain document order; headers, footers,
text boxes and scanned-image OCR are outside this first parser.

Limits: 20 MiB per file, 21 MiB per multipart request (including streaming bodies),
8 MiB extracted text, 2000 chunks, 4000 characters per chunk, 64 MiB decompressed
DOCX and 10000 PDF pages. Parsing runs in a thread, one document at a time per
process; other requests wait without blocking the event loop. This is not a
durable job queue. Go owns retries and job tracking. On cancellation an already
running thread finishes cleanup before its uploaded file is closed.
For hostile documents or strict CPU/memory deadlines, use isolated worker
processes with operating-system resource limits.

Errors: 401 invalid/missing token, 413 upload too large, 415 unsupported format or
MIME mismatch, 422 invalid identifiers/unreadable/empty document or output limit.
Scanned PDFs without text return 422, since Go requires at least one chunk.
Duplicate requests produce deterministic chunks and do not write to Go's database.
Keep the service on loopback/private networking as required by the Go contract.

## RAG generation

POST /internal/v1/rag/generate accepts a query, bounded user/assistant history
and sources already authorized by Go. It shares X-Service-Token with ingestion.
It returns answered, needs_clarification or insufficient_information, with
validated source IDs. Search, ACL and chat storage remain in Go.

Configuration, JSON examples, errors/fallback and quality limits:
[RAG generation contract](../docs/rag-generation-contract.md).
LLM_BASE_URL points to an OpenAI-compatible /v1 origin/path (Ollama supported).
LLM_MODEL must refer to an approved installed model. No model is installed or
downloaded automatically. Without configured inference, nonempty-source requests
return 503; empty-source requests return insufficient_information without a call.

## DOCX templates

POST /internal/v1/templates/inspect discovers required string fields and reports
missing values. POST /internal/v1/templates/render fills the uploaded DOCX and
returns a binary document. Both use the existing X-Service-Token.
Go owns drafts, storage and access control. No LLM is used.

[Template API contract and demo](../docs/templates-contract.md).
Synthetic demo inputs: examples/memo-template.docx and examples/memo-values.json.

## Tests

```powershell
.\.venv\Scripts\python.exe -m pytest -q
```
