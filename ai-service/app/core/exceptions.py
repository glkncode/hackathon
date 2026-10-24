"""Преобразование ошибок сервиса в прежний HTTP-контракт для Go."""

from fastapi import Request
from fastapi.exception_handlers import request_validation_exception_handler
from fastapi.exceptions import RequestValidationError
from starlette.responses import JSONResponse

from app.ingestion.errors import IngestionError


async def ingestion_error(request: Request, exc: IngestionError):
    return JSONResponse({"detail": exc.detail}, status_code=exc.status_code)


async def safe_validation_error(request: Request, exc: RequestValidationError):
    # Ошибки валидации RAG и шаблонов не должны раскрывать содержимое документов.
    if request.url.path == "/internal/v1/rag/generate":
        return JSONResponse({"error": {"code": "invalid_rag_request"}}, status_code=422)
    if request.url.path.startswith("/internal/v1/templates/"):
        return JSONResponse({"error": {"code": "invalid_template_request"}}, status_code=422)
    return await request_validation_exception_handler(request, exc)
