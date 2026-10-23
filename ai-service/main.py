import asyncio
import os
import re
import secrets
from contextlib import asynccontextmanager
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Annotated, BinaryIO
from uuid import UUID
from zipfile import ZipFile

import pymupdf
from docx import Document
from fastapi import FastAPI, File, Form, Header, HTTPException, UploadFile, Request
from fastapi.exceptions import RequestValidationError
from fastapi.exception_handlers import request_validation_exception_handler
from starlette.concurrency import run_in_threadpool
from starlette.responses import JSONResponse

from app.llm.client import LLMClient, LLMConfig, InferenceError
from app.rag.generator import generate
from app.rag.schemas import GenerateRequest, GenerateResponse
from app.templates.routes import router as templates_router
from app.templates.engine import MAX_REQUEST_BYTES as MAX_TEMPLATE_REQUEST_BYTES

MAX_FILE_BYTES = 20 * 1024 * 1024
MAX_REQUEST_BYTES = MAX_FILE_BYTES + 1024 * 1024
MAX_TEXT_BYTES = 8 * 1024 * 1024
MAX_CHUNKS = 2000
CHUNK_CHARS = 4000
MIME_TYPES = {
    ".pdf": "application/pdf",
    ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    ".txt": "text/plain",
}
HEADING = re.compile(r"^\s*(\d+(?:\.\d+)*)(?:[.)])?\s+(.+?)\s*$")


class PrivateIngestMiddleware:
    """Authenticate before multipart parsing and bound streamed request bodies."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or scope["path"] not in {
            "/internal/v1/ingest", "/internal/v1/rag/generate",
            "/internal/v1/templates/inspect", "/internal/v1/templates/render",
        }:
            return await self.app(scope, receive, send)
        expected = scope["app"].state.service_token
        supplied = [value for key, value in scope["headers"] if key.lower() == b"x-service-token"]
        if len(supplied) != 1 or not secrets.compare_digest(
            supplied[0], expected.encode("utf-8")
        ):
            return await JSONResponse(
                {"detail": "Invalid service token"}, status_code=401
            )(scope, receive, send)
        received = 0
        limit = 1024 * 1024 if scope["path"] == "/internal/v1/rag/generate" else MAX_REQUEST_BYTES
        if scope["path"].startswith("/internal/v1/templates/"):
            limit = MAX_TEMPLATE_REQUEST_BYTES

        async def bounded_receive():
            nonlocal received
            message = await receive()
            if message["type"] == "http.request":
                received += len(message.get("body", b""))
                if received > limit:
                    raise HTTPException(413, "Request exceeds upload limit")
            return message

        await self.app(scope, bounded_receive, send)


@asynccontextmanager
async def lifespan(app: FastAPI):
    token = os.environ.get("AI_SERVICE_TOKEN", "")
    if not token.strip():
        raise RuntimeError("Set a nonblank AI_SERVICE_TOKEN before starting the service")
    app.state.service_token = token
    # One parser per process by default, matching the target small VPS.
    app.state.parse_slots = asyncio.Semaphore(1)
    app.state.template_slots = asyncio.Semaphore(2)
    app.state.llm = LLMClient(LLMConfig.from_env())
    try:
        yield
    finally:
        await app.state.llm.close()


app = FastAPI(title="Document parser", lifespan=lifespan)
app.add_middleware(PrivateIngestMiddleware)
app.include_router(templates_router)


@app.exception_handler(RequestValidationError)
async def safe_validation_error(request: Request, exc: RequestValidationError):
    if request.url.path == "/internal/v1/rag/generate":
        return JSONResponse({"error": {"code": "invalid_rag_request"}}, status_code=422)
    if request.url.path.startswith("/internal/v1/templates/"):
        return JSONResponse({"error": {"code": "invalid_template_request"}}, status_code=422)
    return await request_validation_exception_handler(request, exc)


@app.post("/internal/v1/rag/generate", response_model=GenerateResponse)
async def rag_generate(
    body: GenerateRequest,
    x_service_token: Annotated[str | None, Header(alias="X-Service-Token")] = None,
):
    try:
        return await generate(body, app.state.llm)
    except InferenceError as exc:
        return JSONResponse({"error": {"code": exc.code}}, status_code=exc.status_code)


def clipped(value: str, limit: int) -> str:
    return value.encode("utf-8")[:limit].decode("utf-8", errors="ignore")


def make_chunks(blocks) -> list[dict]:
    chunks = []
    total = 0
    section_number = section_title = None
    for page_number, text in blocks:
        # Replace NUL and invalid Unicode before emitting JSON.
        text = text.replace("\x00", "").encode("utf-8", errors="replace").decode("utf-8")
        total += len(text.encode("utf-8"))
        if total > MAX_TEXT_BYTES:
            raise HTTPException(422, "Extracted text exceeds 8 MiB")
        pending = []
        length = 0

        def flush():
            nonlocal pending, length
            content = "\n".join(pending).strip()
            if content:
                if len(chunks) >= MAX_CHUNKS:
                    raise HTTPException(422, "Document exceeds 2000 chunks")
                chunks.append({
                    "chunk_index": len(chunks),
                    "content": content,
                    "page_number": page_number,
                    "section_number": section_number,
                    "section_title": section_title,
                })
            pending, length = [], 0

        for line in text.splitlines():
            line = line.strip()
            if not line:
                continue
            heading = HEADING.fullmatch(line)
            if heading and len(line) <= 250:
                flush()
                section_number = clipped(heading[1], 128)
                section_title = clipped(heading[2], 512)
            for start in range(0, len(line), CHUNK_CHARS):
                piece = line[start:start + CHUNK_CHARS]
                if length + len(piece) + 1 > CHUNK_CHARS:
                    flush()
                pending.append(piece)
                length += len(piece) + 1
        flush()
    if not chunks:
        raise HTTPException(422, "No text extracted; scanned documents require OCR")
    return chunks


def extract_blocks(path: Path, suffix: str):
    if suffix == ".pdf":
        # Memory-backed opening avoids file locks on corrupt PDFs on Windows.
        with pymupdf.open(stream=path.read_bytes(), filetype="pdf") as document:
            if not document.is_pdf or document.needs_pass:
                raise ValueError("Invalid or encrypted PDF")
            if len(document) > 10000:
                raise HTTPException(422, "PDF exceeds 10000 pages")
            for index, page in enumerate(document, start=1):
                yield index, page.get_text()
    elif suffix == ".docx":
        # Bound decompression before python-docx loads XML.
        with ZipFile(path) as archive:
            entries = archive.infolist()
            if len(entries) > 10000 or sum(e.file_size for e in entries) > 64 * 1024 * 1024:
                raise HTTPException(422, "DOCX decompressed size exceeds limit")
        document = Document(path)
        # Preserve paragraph/table order; DOCX has no reliable page numbers.
        for block in document.iter_inner_content():
            if hasattr(block, "rows"):
                for row in block.rows:
                    yield None, "\t".join(cell.text for cell in row.cells)
            else:
                yield None, block.text
    else:
        yield None, path.read_text(encoding="utf-8-sig")


def save_and_parse(source: BinaryIO, suffix: str) -> dict:
    with TemporaryDirectory(prefix="document-ingest-") as directory:
        path = Path(directory) / f"document{suffix}"
        size = 0
        with path.open("wb") as destination:
            while chunk := source.read(1024 * 1024):
                size += len(chunk)
                if size > MAX_FILE_BYTES:
                    raise HTTPException(413, "File exceeds 20 MiB")
                destination.write(chunk)
        if not size:
            raise HTTPException(422, "Empty file")
        try:
            chunks = make_chunks(extract_blocks(path, suffix))
        except HTTPException:
            raise
        except Exception as exc:
            raise HTTPException(422, "Cannot read document; expected PDF, DOCX or UTF-8 TXT") from exc
        return {"chunks": chunks, "warnings": []}


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.post("/internal/v1/ingest")
async def ingest(
    file: Annotated[UploadFile, File()],
    document_id: Annotated[UUID, Form()],
    document_version_id: Annotated[UUID, Form()],
    mime_type: Annotated[str, Form()],
    x_service_token: Annotated[str | None, Header(alias="X-Service-Token")] = None,
):
    try:
        if document_id.int == 0 or document_version_id.int == 0:
            raise HTTPException(422, "Document UUIDs must be nonzero")
        suffix = Path(file.filename or "").suffix.lower()
        if suffix not in MIME_TYPES:
            raise HTTPException(415, "Supported formats: PDF, DOCX, TXT")
        if mime_type.lower().split(";", 1)[0].strip() != MIME_TYPES[suffix]:
            raise HTTPException(415, "MIME type does not match file extension")
        # Thread work finishes before UploadFile is closed, including cancellation.
        async with app.state.parse_slots:
            worker = asyncio.create_task(run_in_threadpool(save_and_parse, file.file, suffix))
            try:
                result = await asyncio.shield(worker)
            except asyncio.CancelledError:
                # A thread cannot be killed safely; let it release its temporary files.
                try:
                    await worker
                except Exception:
                    pass
                raise
        return {"document_version_id": str(document_version_id), **result}
    finally:
        await file.close()
