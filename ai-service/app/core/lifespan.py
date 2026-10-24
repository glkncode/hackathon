"""Создание общих ресурсов при запуске и освобождение при остановке."""

import asyncio
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app.core.config import LLMConfig, PARSE_CONCURRENCY, TEMPLATE_CONCURRENCY
from app.llm.client import LLMClient


@asynccontextmanager
async def lifespan(app: FastAPI):
    token = os.environ.get("AI_SERVICE_TOKEN", "")
    if not token.strip():
        raise RuntimeError("Set a nonblank AI_SERVICE_TOKEN before starting the service")
    app.state.service_token = token
    # Ограничиваем тяжёлые операции в каждом процессе; остальные запросы ожидают.
    app.state.parse_slots = asyncio.Semaphore(PARSE_CONCURRENCY)
    app.state.template_slots = asyncio.Semaphore(TEMPLATE_CONCURRENCY)
    app.state.llm = LLMClient(LLMConfig.from_env())
    try:
        yield
    finally:
        await app.state.llm.close()
