"""Приём файлов от Go и запуск разбора вне цикла событий."""

import asyncio
from pathlib import Path
from typing import Annotated
from uuid import UUID

from fastapi import APIRouter, File, Form, Header, HTTPException, Request, UploadFile
from starlette.concurrency import run_in_threadpool

from app.core.config import MIME_TYPES
from app.ingestion.service import save_and_parse
from app.schemas.ingestion import IngestResponse

router = APIRouter()


@router.post("/internal/v1/ingest", response_model=IngestResponse)
async def ingest(
    request: Request,
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
        # Не закрываем загруженный файл, пока поток разбора ещё использует его.
        async with request.app.state.parse_slots:
            worker = asyncio.create_task(run_in_threadpool(save_and_parse, file.file, suffix))
            try:
                result = await asyncio.shield(worker)
            except asyncio.CancelledError:
                # Поток нельзя безопасно прервать: ждём очистки временных файлов.
                try:
                    await worker
                except Exception:
                    pass
                raise
        return {"document_version_id": str(document_version_id), **result}
    finally:
        await file.close()
