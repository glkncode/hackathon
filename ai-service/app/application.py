"""Сборка FastAPI-приложения из ручек и общих обработчиков."""

from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError

from app.api import health, ingestion, rag, templates
from app.core.exceptions import ingestion_error, safe_validation_error
from app.core.lifespan import lifespan
from app.core.middleware import PrivateAPIMiddleware
from app.ingestion.errors import IngestionError


def create_app() -> FastAPI:
    app = FastAPI(
        title="Обработка документов и ответы по регламентам",
        description=(
            "Внутренний Python-сервис: разбор PDF, DOCX и TXT, ответы нейронки по источникам "
            "и заполнение DOCX-шаблонов.\n\n"
            "**Как проверить:** раскройте нужную ручку, нажмите **Try it out**, заполните поля "
            "и нажмите **Execute**. Во всех внутренних ручках нужен заголовок "
            "`X-Service-Token` — значение `AI_SERVICE_TOKEN` из локального `.env`.\n\n"
            "В обычной работе эти ручки вызывает Go. Он отвечает за пользователей, права, "
            "хранение документов, поиск источников и историю чата."
        ),
        openapi_tags=[
            {"name": "Состояние сервиса", "description": "Проверка доступности Python-сервиса."},
            {"name": "Разбор документов", "description": "Загрузка файла и извлечение текстовых фрагментов."},
            {"name": "Ответы нейронки", "description": "Генерация ответа по переданным источникам."},
            {"name": "Шаблоны документов", "description": "Проверка полей и заполнение DOCX-шаблонов."},
        ],
        lifespan=lifespan,
    )
    app.add_middleware(PrivateAPIMiddleware)
    app.add_exception_handler(RequestValidationError, safe_validation_error)
    app.add_exception_handler(IngestionError, ingestion_error)
    app.include_router(health.router)
    app.include_router(ingestion.router)
    app.include_router(rag.router)
    app.include_router(templates.router)
    return app
