"""HTTP-ручки проверки и заполнения DOCX-шаблонов."""

from typing import Annotated

from fastapi import APIRouter, File, Form, Header, Request, UploadFile
from starlette.concurrency import run_in_threadpool
from starlette.responses import JSONResponse, Response

from app.core.config import MAX_TEMPLATE_BYTES, MIME_TYPES
from app.schemas.templates import Inspection
from app.templates.engine import TemplateError, process_template

DOCX_MIME = MIME_TYPES[".docx"]
router = APIRouter(prefix="/internal/v1/templates", tags=["templates"])


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


@router.post("/inspect", response_model=Inspection)
async def inspect_template(
    request: Request,
    template: Annotated[UploadFile, File()],
    values: Annotated[str, Form(max_length=65536)] = "{}",
    x_service_token: Annotated[str | None, Header(alias="X-Service-Token")] = None,
):
    return await handle(request, template, values, False)


@router.post("/render", response_class=Response, responses={
    200: {"content": {DOCX_MIME: {"schema": {"type": "string", "format": "binary"}}}},
    422: {"description": "Invalid template/values or missing fields"},
})
async def render_template(
    request: Request,
    template: Annotated[UploadFile, File()],
    values: Annotated[str, Form(max_length=65536)],
    x_service_token: Annotated[str | None, Header(alias="X-Service-Token")] = None,
):
    return await handle(request, template, values, True)
