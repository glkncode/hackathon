"""Контракт результата разбора документа для Go-сервиса."""

from uuid import UUID

from pydantic import BaseModel, Field


class DocumentChunk(BaseModel):
    chunk_index: int = Field(description="Порядковый номер фрагмента, начиная с нуля")
    content: str = Field(description="Извлечённый текст фрагмента")
    page_number: int | None = Field(description="Страница PDF, начиная с 1; null для DOCX и TXT")
    section_number: str | None = Field(description="Номер раздела, если удалось определить")
    section_title: str | None = Field(description="Заголовок раздела, если удалось определить")


class IngestResponse(BaseModel):
    document_version_id: UUID = Field(description="UUID обработанной версии, полученный от Go")
    chunks: list[DocumentChunk] = Field(description="Текстовые фрагменты для сохранения и поиска в Go")
    warnings: list[str] = Field(description="Предупреждения обработки; пустой список, если их нет")
