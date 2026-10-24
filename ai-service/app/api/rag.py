"""Генерация ответа по вопросу и источникам, которые передал Go."""

from typing import Annotated

from fastapi import APIRouter, Header, Request
from starlette.responses import JSONResponse

from app.llm.client import InferenceError
from app.rag.generator import generate
from app.schemas.rag import GenerateRequest, GenerateResponse

router = APIRouter(tags=["Ответы нейронки"])


@router.post(
    "/internal/v1/rag/generate",
    response_model=GenerateResponse,
    summary="Получить ответ нейронки по источникам",
    description=(
        "Передайте вопрос (`query`), историю (`history`) и фрагменты документов (`sources`). "
        "В обычной работе источники находит Go с учётом прав пользователя. "
        "В Swagger их нужно указать вручную — эта ручка сама не ищет загруженные документы.\n\n"
        "Результат: `answered` — ответ с идентификаторами источников; "
        "`needs_clarification` — уточняющий вопрос; "
        "`insufficient_information` — недостаточно информации. "
        "Если `sources` пуст, модель не вызывается."
    ),
    response_description="Проверенный ответ модели, источники или уточняющий вопрос",
    responses={
        401: {"description": "Служебный токен отсутствует или неверен"},
        413: {"description": "Превышен размер запроса"},
        422: {"description": "Неверная структура запроса или превышены ограничения контекста"},
        502: {"description": "Модель вернула некорректный ответ или ошибку протокола"},
        503: {"description": "Модель недоступна или не настроена"},
        504: {"description": "Истекло время ожидания ответа модели"},
    },
)
async def rag_generate(
    request: Request,
    body: GenerateRequest,
    x_service_token: Annotated[str | None, Header(
        alias="X-Service-Token", description="Служебный токен: AI_SERVICE_TOKEN из локального .env"
    )] = None,
):
    try:
        return await generate(body, request.app.state.llm)
    except InferenceError as exc:
        return JSONResponse({"error": {"code": exc.code}}, status_code=exc.status_code)
