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

router = APIRouter(tags=["Разбор документов"])


@router.post(
    "/internal/v1/ingest",
    response_model=IngestResponse,
    summary="Загрузить и разобрать документ",
    description=(
        "Принимает **PDF, DOCX или UTF-8 TXT** размером до **20 МиБ** и возвращает текстовые фрагменты. "
        "Для PDF сохраняются номера страниц; для DOCX и TXT номер страницы равен `null`. "
        "Сканы без текстового слоя требуют OCR, который пока не поддерживается.\n\n"
        "Для ручной проверки укажите два непустых UUID (документа и версии), выберите файл "
        "и задайте `mime_type`: `application/pdf`, "
        "`application/vnd.openxmlformats-officedocument.wordprocessingml.document` или `text/plain`.\n\n"
        "Python удаляет временный файл после обработки. Хранением оригиналов, версий и фрагментов занимается Go."
    ),
    response_description="Версия документа, извлечённые фрагменты и предупреждения",
    responses={
        401: {"description": "Служебный токен отсутствует или неверен"},
        413: {"description": "Превышен допустимый размер файла или запроса"},
        415: {"description": "Неподдерживаемый формат или MIME-тип не соответствует расширению"},
        422: {"description": "Неверные поля, пустой/повреждённый документ или превышены ограничения разбора"},
    },
)
async def ingest(
    request: Request,
    file: Annotated[UploadFile, File(description="Документ PDF, DOCX или UTF-8 TXT, до 20 МиБ")],
    document_id: Annotated[UUID, Form(description="UUID документа из Go; для ручной проверки — любой UUID, отличный от нулевого")],
    document_version_id: Annotated[UUID, Form(description="UUID версии документа, отличный от нулевого")],
    mime_type: Annotated[str, Form(description="Тип файла: application/pdf, text/plain или MIME-тип DOCX из описания ручки")],
    x_service_token: Annotated[str | None, Header(
        alias="X-Service-Token", description="Служебный токен: AI_SERVICE_TOKEN из локального .env"
    )] = None,
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
