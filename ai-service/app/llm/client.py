"""Асинхронный клиент OpenAI-совместимого API модели."""

import asyncio
import json

import httpx

from app.core.config import LLMConfig, LLM_CONCURRENCY, MAX_RESPONSE_BYTES


class InferenceError(Exception):
    def __init__(self, code: str, status_code: int):
        self.code = code
        self.status_code = status_code


class LLMClient:
    """Отправляет запросы модели и ограничивает параллельные обращения."""

    def __init__(self, config: LLMConfig):
        self.config = config
        self.http = httpx.AsyncClient(
            timeout=config.timeout, trust_env=False, follow_redirects=False,
            limits=httpx.Limits(max_connections=LLM_CONCURRENCY, max_keepalive_connections=LLM_CONCURRENCY),
        )
        self.slots = asyncio.Semaphore(LLM_CONCURRENCY)

    async def close(self):
        await self.http.aclose()

    async def generate(self, messages: list[dict], *, response_schema: dict | None = None) -> str:
        if not self.config.base_url or not self.config.model:
            raise InferenceError("llm_unavailable", 503)
        headers = {}
        if self.config.api_key:
            headers["Authorization"] = "Bearer " + self.config.api_key
        payload = {
            "model": self.config.model,
            "messages": messages,
            "temperature": 0,
            "max_tokens": 4096,
            "stream": False,
            "response_format": {"type": "json_object"},
        }
        if response_schema is not None:
            payload["response_format"] = {"type": "json_schema", "json_schema": {
                "name": "document_interpretation", "strict": False, "schema": response_schema,
            }}
        try:
            async with asyncio.timeout(self.config.timeout):
                async with self.slots:
                    async with self.http.stream(
                        "POST", self.config.base_url + "/chat/completions",
                        headers=headers, json=payload,
                    ) as response:
                        if response.status_code == 429 or response.status_code >= 500:
                            raise InferenceError("llm_unavailable", 503)
                        if response.status_code != 200:
                            raise InferenceError("llm_upstream_error", 502)
                        if response.headers.get("content-type", "").split(";")[0] != "application/json":
                            raise InferenceError("llm_invalid_response", 502)
                        data = bytearray()
                        async for part in response.aiter_bytes():
                            data.extend(part)
                            if len(data) > MAX_RESPONSE_BYTES:
                                raise InferenceError("llm_invalid_response", 502)
                        body = json.loads(data)
                        choice = body["choices"][0]
                        if choice.get("finish_reason") != "stop":
                            raise ValueError()
                        message = choice["message"]
                        if message.get("tool_calls") or not isinstance(message["content"], str):
                            raise ValueError()
                        return message["content"]
        except (TimeoutError, httpx.TimeoutException):
            raise InferenceError("llm_timeout", 504) from None
        except httpx.RequestError:
            raise InferenceError("llm_unavailable", 503) from None
        except (ValueError, KeyError, IndexError, TypeError):
            raise InferenceError("llm_invalid_response", 502) from None
