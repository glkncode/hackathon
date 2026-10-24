"""Проверка доступности HTTP-сервиса для Docker."""

from fastapi import APIRouter

router = APIRouter()


@router.get("/health")
async def health():
    return {"status": "ok"}
