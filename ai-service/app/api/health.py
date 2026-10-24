"""Проверка доступности HTTP-сервиса для Docker."""

from fastapi import APIRouter

router = APIRouter(tags=["Состояние сервиса"])


@router.get(
    "/health",
    summary="Проверить доступность сервиса",
    description="Возвращает `status: ok`, если HTTP-сервис работает. Не проверяет Go, базу данных или модель.",
    response_description="Сервис доступен",
)
async def health():
    return {"status": "ok"}
