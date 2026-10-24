"""HTTP-ручки проверки и заполнения DOCX-шаблонов."""

from typing import Annotated

from fastapi import APIRouter, File, Form, Header, Request, UploadFile
from starlette.concurrency import run_in_threadpool
from starlette.responses import JSONResponse, Response

from app.core.config import MAX_TEMPLATE_BYTES, MIME_TYPES
from app.schemas.templates import Inspection
from app.templates.engine import TemplateError, process_template

DOCX_MIME = MIME_TYPES[".docx"]
router = APIRouter(
    prefix="/internal/v1/templates",
    tags=["Шаблоны документов"],
    responses={
        401: {"description": "Служебный токен отсутствует или неверен"},
        413: {"description": "Превышен размер шаблона, значений или результата"},
        415: {"description": "Шаблон должен иметь расширение .docx"},
        422: {"description": "Неверный шаблон, значения полей или неподдерживаемые конструкции"},
    },
)


async def handle(request: Request, template: UploadFile, values: str, render: bool):
    try:
        if not (template.filename or "").lower().endswith(".docx"):
            raise TemplateError("unsupported_template_format", 415)
        content = await template.read(MAX_TEMPLATE_BYTES + 1)
        async with request.app.state.template_slots:
            result = await run_in_threadpool(process_template, content, values, render)
        if render:
            return Response(result, media_type=DOCX_MIME, headers={
                "Content-Disposition": 'attachment; filename="document.docx"',
                "Cache-Control": "no-store",
                "X-Content-Type-Options": "nosniff",
            })
        return result
    except TemplateError as exc:
        detail = {"code": exc.code}
        if exc.missing_fields is not None:
            detail["missing_fields"] = exc.missing_fields
        return JSONResponse({"error": detail}, status_code=exc.status, headers={"Cache-Control": "no-store"})
    finally:
        await template.close()


@router.post(
    "/inspect",
    response_model=Inspection,
    summary="Проверить поля DOCX-шаблона",
    description=(
        "Загрузите DOCX до **5 МиБ** с полями вида `{{ recipient }}`. "
        "Имена полей — латинские строчные буквы, цифры и подчёркивания, начиная с буквы. "
        "В `values` можно передать JSON со строковыми значениями, например "
        '`{"recipient": "Иванов Иван Иванович"}`. '
        "Если значения ещё не известны, оставьте `{}`. "
        "Ответ покажет все поля, недостающие значения и готовность к заполнению. Модель не вызывается."
    ),
    response_description="Список полей шаблона и отсутствующих значений",
)
async def inspect_template(
    request: Request,
    template: Annotated[UploadFile, File(description="DOCX-шаблон с полями {{ name }}, до 5 МиБ")],
    values: Annotated[str, Form(max_length=65536, description="JSON-объект со строковыми значениями полей; {} — проверить без значений")] = "{}",
    x_service_token: Annotated[str | None, Header(
        alias="X-Service-Token", description="Служебный токен: AI_SERVICE_TOKEN из локального .env"
    )] = None,
):
    return await handle(request, template, values, False)


@router.post(
    "/render",
    response_class=Response,
    summary="Заполнить шаблон и скачать DOCX",
    description=(
        "Загрузите DOCX-шаблон и передайте JSON со значениями **всех** его полей в `values`, "
        'например `{"recipient": "Иванов Иван Иванович"}`. '
        "Значения должны быть строками; пустые обязательные и неизвестные поля отклоняются. "
        "Результат — готовый DOCX-файл для скачивания. Модель не вызывается."
    ),
    response_description="Готовый документ DOCX",
    responses={
        200: {"description": "Готовый документ DOCX", "content": {DOCX_MIME: {"schema": {"type": "string", "format": "binary"}}}},
        422: {"description": "Неверный шаблон, значения или не заполнены обязательные поля"},
    },
)
async def render_template(
    request: Request,
    template: Annotated[UploadFile, File(description="DOCX-шаблон с полями {{ name }}, до 5 МиБ")],
    values: Annotated[str, Form(max_length=65536, description="JSON-объект со строковыми значениями всех полей шаблона")],
    x_service_token: Annotated[str | None, Header(
        alias="X-Service-Token", description="Служебный токен: AI_SERVICE_TOKEN из локального .env"
    )] = None,
):
    return await handle(request, template, values, True)
