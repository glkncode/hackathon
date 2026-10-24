# Working group ingestion fixture

`working_group_ingestion.json` is the unchanged Python pipeline's output for
`data/demo/*создании_рабоч*.docx`. It includes all 36 chunks, complete tab-separated
table rows, empty fields, and the actual inherited section metadata. It contains
no database IDs or credentials. `source_sha256` binds it to the original DOCX;
Go regression tests fail if that document changes.

Generated from the repository root using the existing Python functions:

```python
import hashlib
import json
import sys
from pathlib import Path

sys.path.insert(0, "ai-service")
from app.ingestion.parsers.docx import extract_docx
from app.chunking.text import make_chunks

source = next(Path("data/demo").glob("*создании_рабоч*.docx"))
fixture = {
    "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
    "chunks": make_chunks(extract_docx(source)),
}
Path("tests/integration/testdata/working_group_ingestion.json").write_text(
    json.dumps(fixture, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
)
```

Use the project's Python virtual environment. Regeneration is a diagnostic
operation, not a change to the parser or business document. Inspect any diff
before updating chunk-index expectations. `TestIntegrationWorkingGroupQwen`
additionally sends the actual DOCX through the production Go multipart client
to the real Python ingestion endpoint and compares all text/metadata to this
fixture before persisting and searching the returned chunks.
