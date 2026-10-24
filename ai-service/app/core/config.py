"""Настройки окружения и ограничения ресурсов сервиса."""

import ipaddress
import math
import os
from dataclasses import dataclass
from urllib.parse import urlsplit

MAX_FILE_BYTES = 20 * 1024 * 1024
MAX_REQUEST_BYTES = MAX_FILE_BYTES + 1024 * 1024
MAX_TEXT_BYTES = 8 * 1024 * 1024
MAX_CHUNKS = 2000
CHUNK_CHARS = 4000
PARSE_CONCURRENCY = 1
TEMPLATE_CONCURRENCY = 2
LLM_CONCURRENCY = 4
MAX_RESPONSE_BYTES = 128 * 1024
MAX_TEMPLATE_BYTES = 5 * 1024 * 1024
MAX_TEMPLATE_REQUEST_BYTES = MAX_TEMPLATE_BYTES + 1024 * 1024
MAX_VALUES_BYTES = 64 * 1024
MIME_TYPES = {
    ".pdf": "application/pdf",
    ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    ".txt": "text/plain",
}

# Проверяем токен до разбора тела, а размер ограничиваем даже без Content-Length.
PRIVATE_REQUEST_LIMITS = {
    "/internal/v1/embeddings": 300 * 1024,
    "/internal/v1/evidence": 128 * 1024,
    "/internal/v1/ingest": MAX_REQUEST_BYTES,
    "/internal/v1/rag/generate": 1024 * 1024,
    "/internal/v1/templates/inspect": MAX_TEMPLATE_REQUEST_BYTES,
    "/internal/v1/templates/render": MAX_TEMPLATE_REQUEST_BYTES,
}


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

                # Для сервера вне localhost требуется явное LLM_ALLOW_REMOTE=true.
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
