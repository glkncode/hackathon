"""Контракт результата разбора документа для Go-сервиса."""

from uuid import UUID

from pydantic import BaseModel


class DocumentChunk(BaseModel):
    chunk_index: int
    content: str
    page_number: int | None
    section_number: str | None
    section_title: str | None


class IngestResponse(BaseModel):
    document_version_id: UUID
    chunks: list[DocumentChunk]
    warnings: list[str]
