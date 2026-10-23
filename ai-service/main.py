import asyncio
from contextlib import asynccontextmanager
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Annotated, BinaryIO

import pymupdf
from docx import Document
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from starlette.concurrency import run_in_threadpool

MAX_FILE_BYTES = 20 * 1024 * 1024


@asynccontextmanager
async def lifespan(app: FastAPI):
    app.state.parse_slots = asyncio.Semaphore(4)
    yield


app = FastAPI(title="Document parser", lifespan=lifespan)


def save_and_read(source: BinaryIO, suffix: str) -> dict:
    # Every request has its own directory, removed on success or failure.
    with TemporaryDirectory(prefix="document-ingest-") as directory:
        path = Path(directory) / f"document{suffix}"
        size = 0
        with path.open("wb") as destination:
            while chunk := source.read(1024 * 1024):
                size += len(chunk)
                if size > MAX_FILE_BYTES:
                    raise HTTPException(413, "File exceeds 20 MiB")
                destination.write(chunk)
        if size == 0:
            raise HTTPException(400, "Empty file")
        try:
            if suffix == ".pdf":
                with pymupdf.open(stream=path.read_bytes(), filetype="pdf") as document:
                    if not document.is_pdf or document.needs_pass:
                        raise ValueError("Invalid or encrypted PDF")
                    text = "\n\n".join(page.get_text() for page in document)
            elif suffix == ".docx":
                document = Document(path)
                parts = [paragraph.text for paragraph in document.paragraphs]
                parts.extend(
                    "\t".join(cell.text for cell in row.cells)
                    for table in document.tables
                    for row in table.rows
                )
                text = "\n".join(parts)
            else:
                text = path.read_text(encoding="utf-8-sig")
        except Exception as exc:
            raise HTTPException(
                422, "Cannot read document: use a valid PDF, DOCX or UTF-8 TXT"
            ) from exc
        return {
            "size_bytes": size,
            "text": text,
            "warnings": [] if text.strip() else ["No text extracted; OCR may be required"],
        }


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.post("/internal/v1/ingest")
async def ingest(
    file: Annotated[UploadFile, File()],
    document_id: Annotated[str, Form(min_length=1)],
):
    try:
        suffix = Path(file.filename or "").suffix.lower()
        if suffix not in {".pdf", ".docx", ".txt"}:
            raise HTTPException(415, "Supported formats: PDF, DOCX, TXT")
        async with app.state.parse_slots:
            result = await run_in_threadpool(save_and_read, file.file, suffix)
        return {"document_id": document_id, "status": "completed", **result}
    finally:
        await file.close()
