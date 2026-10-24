"""Сборка FastAPI-приложения из ручек и общих обработчиков."""

from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError

from app.api import health, ingestion, rag, templates
from app.core.exceptions import ingestion_error, safe_validation_error
from app.core.lifespan import lifespan
from app.core.middleware import PrivateAPIMiddleware
from app.ingestion.errors import IngestionError


def create_app() -> FastAPI:
    app = FastAPI(title="Document parser", lifespan=lifespan)
    app.add_middleware(PrivateAPIMiddleware)
    app.add_exception_handler(RequestValidationError, safe_validation_error)
    app.add_exception_handler(IngestionError, ingestion_error)
    app.include_router(health.router)
    app.include_router(ingestion.router)
    app.include_router(rag.router)
    app.include_router(templates.router)
    return app
