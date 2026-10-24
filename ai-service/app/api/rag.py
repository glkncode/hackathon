"""Генерация ответа по вопросу и источникам, которые передал Go."""

from typing import Annotated

from fastapi import APIRouter, Header, Request
from starlette.responses import JSONResponse

from app.llm.client import InferenceError
from app.rag.generator import generate
from app.schemas.rag import GenerateRequest, GenerateResponse

router = APIRouter()


@router.post("/internal/v1/rag/generate", response_model=GenerateResponse)
async def rag_generate(
    request: Request,
    body: GenerateRequest,
    x_service_token: Annotated[str | None, Header(alias="X-Service-Token")] = None,
):
    try:
        return await generate(body, request.app.state.llm)
    except InferenceError as exc:
        return JSONResponse({"error": {"code": exc.code}}, status_code=exc.status_code)
