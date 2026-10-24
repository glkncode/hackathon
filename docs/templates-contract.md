# Private DOCX template API

Python inspects and fills an uploaded template using docxtpl. Go owns template
storage, ACL, template versions, drafts, collecting missing values and delivery.
No LLM, search, database writes or network calls are used by the template engine.
Python keeps request-local buffers only and does not store templates/results.

Both endpoints require X-Service-Token equal to AI_SERVICE_TOKEN, as ingestion.
Requests use multipart/form-data. Uploads with unknown Content-Length are accepted.

| Field | inspect | render |
| --- | --- | --- |
| template | Required DOCX file | Required same DOCX file |
| values | Optional JSON object as a form string, defaults to {} | Required JSON object as a form string |

Do not send values as a JSON file upload or as the overall request body.
Use the same authorized template version for inspect and render.

## Template language

Use simple, flat string fields such as {{ recipient_name }}. Field identifiers
match [a-z][a-z0-9_]{0,63}. Every discovered field is required in this first
version. Values must be strings; missing/blank/whitespace-only values require
clarification. Numbers, dates and booleans must be formatted by Go as strings.
Unknown keys and duplicate JSON keys are rejected so typos are not ignored.
Field order is stable and alphabetical.

Placeholders work in paragraphs, tables, headers, footers and footnotes.
Ordinary Word runs may split a placeholder; docxtpl repairs them.
Jinja control flow, expressions, filters, attribute access, calls, includes and
docxtpl rich-text instructions are not supported. There are no optional/default
values or type-specific validators yet. Go can map field names to human-readable
labels or clarification questions.

Uploaded DOCX ZIP/XML is validated before processing. Macros, embedded objects,
ActiveX, external relationships (including external hyperlinks/images), field
codes, DTD/entities, unsafe ZIP paths and placeholders in unsupported parts or
attributes are rejected. Use static plain DOCX templates, not arbitrary Office
files. Values are escaped as XML and never evaluated again as Jinja.
This is a restricted renderer, not a general Jinja execution service or antivirus.

Limits: 5 MiB compressed template, 6 MiB whole request, 1000 ZIP members, 32 MiB
expanded content, 64 fields, 64 KiB UTF-8 values JSON, 8000 characters per value,
10 MiB output. At most two template jobs run per process in worker threads.
Waiting requests do not block the async event loop. Other endpoints remain usable.

## Inspect

POST /internal/v1/templates/inspect

With a memo containing {{ recipient_name }} and {{ subject }}, passing
values={"subject":"Выделение оборудования"} returns HTTP 200:

```json
{
  "fields": [
    {"name": "recipient_name", "type": "string", "required": true},
    {"name": "subject", "type": "string", "required": true}
  ],
  "required_fields": ["recipient_name", "subject"],
  "missing_fields": ["recipient_name"],
  "can_render": false
}
```

Go asks the user for recipient_name, saves the draft on its side, then resends
the template with the complete values object. Python does not infer or invent data.

## Render

POST /internal/v1/templates/render

HTTP 200 returns binary DOCX, not JSON or base64:

```http
Content-Type: application/vnd.openxmlformats-officedocument.wordprocessingml.document
Content-Disposition: attachment; filename="document.docx"
Cache-Control: no-store
X-Content-Type-Options: nosniff
```

Missing required values return HTTP 422:

```json
{"error": {"code": "missing_fields", "missing_fields": ["recipient_name"]}}
```

Go should inspect the HTTP code and content type before saving the response.
Only successful binary results are documents. Python neither signs nor publishes
the generated memo. The handwritten-signature line in the demo is left blank.

## Errors

| HTTP | Code |
| --- | --- |
| 401 | Existing {"detail":"Invalid service token"} authentication error |
| 413 | template_too_large, template_expanded_limit, values_too_large, rendered_document_too_large |
| 413 | {"detail":"Request exceeds upload limit"} for whole request limit |
| 415 | unsupported_template_format |
| 422 | invalid_template_request for missing/invalid multipart fields |
| 422 | invalid_template, unsafe_template, unsupported_template_syntax, unsupported_template_part |
| 422 | invalid_values, unknown_fields, too_many_fields, missing_fields |

Errors do not include XML, field values, paths, credentials or document contents.
Do not log uploaded templates, values or DOCX output in Go/proxies.

## Local demo

Start the existing service from ai-service (same token as Go):

```powershell
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
.\.venv\Scripts\python.exe -c "from dotenv import load_dotenv; load_dotenv('.env'); import uvicorn; uvicorn.run('main:app', host='127.0.0.1', port=8000)"
```

Swagger: http://127.0.0.1:8000/docs, templates section.
Upload examples/memo-template.docx. Leave values={} for inspect to see missing
fields; paste examples/memo-values.json to inspect a complete set or render.
The filled demo uses synthetic organization/person names and is not an official form.

```powershell
# Read token without printing it.
$env:AI_SERVICE_TOKEN = (Get-Content .env | Where-Object { $_ -match '^AI_SERVICE_TOKEN=' } | Select-Object -First 1) -replace '^AI_SERVICE_TOKEN=', ''

curl.exe http://127.0.0.1:8000/internal/v1/templates/inspect -H "X-Service-Token: $env:AI_SERVICE_TOKEN" -F "template=@examples/memo-template.docx"

curl.exe http://127.0.0.1:8000/internal/v1/templates/render -H "X-Service-Token: $env:AI_SERVICE_TOKEN" -F "template=@examples/memo-template.docx" -F "values=<examples/memo-values.json" --output memo.docx
```

Check HTTP status before opening output; curl saves JSON errors to the output
file too. Swagger displays the response status and offers Download file on success.

Tests from ai-service:

```powershell
.\.venv\Scripts\python.exe -m pytest -q
```

Template tests cover inspect/render, missing values, malformed values and DOCX,
split runs, headers/footers/tables, XML escaping, token checks, unsafe archives,
Jinja execution attempts, limits, concurrent isolation and the demo template.
