import asyncio
import ipaddress
import json
import math
import os
from dataclasses import dataclass
from urllib.parse import urlsplit

import httpx

MAX_RESPONSE_BYTES = 128 * 1024


class InferenceError(Exception):
    def __init__(self, code: str, status_code: int):
        self.code = code
        self.status_code = status_code


@dataclass(frozen=True)
class LLMConfig:
    base_url: str = ""
    model: str = ""
    api_key: str = ""
    timeout: float = 60

    @classmethod
    def from_env(cls):
        base = os.environ.get("LLM_BASE_URL", "").strip().rstrip("/")
        model = os.environ.get("LLM_MODEL", "").strip()
        try:
            timeout = float(os.environ.get("LLM_TIMEOUT", "60"))
            if not math.isfinite(timeout) or not 0 < timeout <= 300:
                raise ValueError()
            if base:
                url = urlsplit(base)
                if (url.scheme not in {"http", "https"} or not url.hostname
                        or url.username or url.password or url.query or url.fragment):
                    raise ValueError()
                _ = url.port
                # Explicit operator opt-in is required before using nonlocal hosts.
                local = url.hostname in {"localhost", "host.docker.internal", "ollama"}
                try:
                    local = local or ipaddress.ip_address(url.hostname).is_loopback
                except ValueError:
                    pass
                if not local and os.environ.get("LLM_ALLOW_REMOTE") != "true":
                    raise ValueError()
        except ValueError:
            raise RuntimeError("Invalid LLM configuration; remote inference requires explicit approval") from None
        return cls(base, model, os.environ.get("LLM_API_KEY", ""), timeout)


class LLMClient:
    """OpenAI-compatible chat protocol, including Ollama's /v1 endpoint."""

    def __init__(self, config: LLMConfig):
        self.config = config
        self.http = httpx.AsyncClient(
            timeout=config.timeout, trust_env=False, follow_redirects=False,
            limits=httpx.Limits(max_connections=4, max_keepalive_connections=4),
        )
        self.slots = asyncio.Semaphore(4)

    async def close(self):
        await self.http.aclose()

    async def generate(self, messages: list[dict]) -> str:
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
