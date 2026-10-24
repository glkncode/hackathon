"""Разбиение текста на фрагменты с сохранением страниц и разделов."""

import re
from collections.abc import Iterable

from app.core.config import CHUNK_CHARS, MAX_CHUNKS, MAX_TEXT_BYTES
from app.ingestion.errors import IngestionError

HEADING = re.compile(r"^\s*(\d+(?:\.\d+)*)(?:[.)])?\s+(.+?)\s*$")


def clipped(value: str, limit: int) -> str:
    return value.encode("utf-8")[:limit].decode("utf-8", errors="ignore")


def make_chunks(blocks: Iterable[tuple[int | None, str]]) -> list[dict]:
    chunks = []
    total = 0
    section_number = section_title = None
    for page_number, text in blocks:
        # Удаляем NUL и некорректные символы перед сериализацией в JSON.
        text = text.replace("\x00", "").encode("utf-8", errors="replace").decode("utf-8")
        total += len(text.encode("utf-8"))
        if total > MAX_TEXT_BYTES:
            raise IngestionError(422, "Extracted text exceeds 8 MiB")
        pending = []
        length = 0

        def flush():
            nonlocal pending, length
            content = "\n".join(pending).strip()
            if content:
                if len(chunks) >= MAX_CHUNKS:
                    raise IngestionError(422, "Document exceeds 2000 chunks")
                chunks.append({
                    "chunk_index": len(chunks),
                    "content": content,
                    "page_number": page_number,
                    "section_number": section_number,
                    "section_title": section_title,
                })
            pending, length = [], 0

        for line in text.splitlines():
            line = line.strip()
            if not line:
                continue
            heading = HEADING.fullmatch(line)
            if heading and len(line) <= 250:
                flush()
                section_number = clipped(heading[1], 128)
                section_title = clipped(heading[2], 512)
            for start in range(0, len(line), CHUNK_CHARS):
                piece = line[start:start + CHUNK_CHARS]
                if length + len(piece) + 1 > CHUNK_CHARS:
                    flush()
                pending.append(piece)
                length += len(piece) + 1
        flush()
    if not chunks:
        raise IngestionError(422, "No text extracted; scanned documents require OCR")
    return chunks
