"""Проверка служебного токена и размера тела внутренних запросов."""

import secrets

from fastapi import HTTPException
from starlette.responses import JSONResponse

from app.core.config import PRIVATE_REQUEST_LIMITS


class PrivateAPIMiddleware:
    """Проверяет доступ до разбора JSON и загружаемых файлов."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or scope["path"] not in PRIVATE_REQUEST_LIMITS:
            return await self.app(scope, receive, send)
        expected = scope["app"].state.service_token
        supplied = [value for key, value in scope["headers"] if key.lower() == b"x-service-token"]
        if len(supplied) != 1 or not secrets.compare_digest(supplied[0], expected.encode("utf-8")):
            return await JSONResponse(
                {"detail": "Invalid service token"}, status_code=401
            )(scope, receive, send)
        received = 0
        limit = PRIVATE_REQUEST_LIMITS[scope["path"]]

        async def bounded_receive():
            nonlocal received
            message = await receive()
            if message["type"] == "http.request":
                received += len(message.get("body", b""))
                if received > limit:
                    raise HTTPException(413, "Request exceeds upload limit")
            return message

        await self.app(scope, bounded_receive, send)
