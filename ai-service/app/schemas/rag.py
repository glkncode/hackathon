"""Схемы вопроса, истории, источников и проверенного ответа модели."""

import json
from typing import Annotated, Literal
from uuid import UUID

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)


Text = Annotated[
    str, StringConstraints(strip_whitespace=True, min_length=1, max_length=4000)
]
SourceText = Annotated[
    str, StringConstraints(strip_whitespace=True, min_length=1, max_length=16000)
]


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")

    @model_validator(mode="before")
    @classmethod
    def valid_unicode(cls, data):
        def check(value):
            if isinstance(value, str):
                value.encode("utf-8")
                if "\x00" in value:
                    raise ValueError("NUL is not allowed")
            elif isinstance(value, dict):
                for key, item in value.items():
                    check(key)
                    check(item)
            elif isinstance(value, list):
                for item in value:
                    check(item)
        try:
            check(data)
        except UnicodeError:
            raise ValueError("Invalid Unicode") from None
        return data


class HistoryMessage(StrictModel):
    role: Literal["user", "assistant"] = Field(description="Автор сообщения: user — пользователь, assistant — помощник")
    content: Text = Field(description="Текст сообщения из истории диалога, до 4000 символов")


class Source(StrictModel):
    chunk_id: UUID = Field(description="Уникальный UUID фрагмента; используется для ссылок в ответе")
    document_title: Annotated[
        str, StringConstraints(strip_whitespace=True, min_length=1, max_length=512)
    ] = Field(description="Название документа-источника")
    version_number: Annotated[
        int, Field(strict=True, ge=1)
    ] = Field(description="Номер версии документа, начиная с 1")
    section_number: Annotated[
        str, StringConstraints(max_length=128)
    ] | None = Field(default=None, description="Номер раздела или null, если не определён")
    page_number: Annotated[
        int, Field(strict=True, ge=1, le=1000000)
    ] | None = Field(default=None, description="Номер страницы, начиная с 1, или null")
    content: SourceText = Field(description="Текст фрагмента, на основании которого модель отвечает на вопрос")


class GenerateRequest(StrictModel):
    model_config = ConfigDict(json_schema_extra={
        "examples": [{
            "query": "Кто согласует служебную записку?",
            "history": [],
            "sources": [{
                "chunk_id": "11111111-1111-4111-8111-111111111111",
                "document_title": "Пример регламента согласования",
                "version_number": 1,
                "section_number": "4.6",
                "page_number": 13,
                "content": "Служебную записку согласует руководитель подразделения.",
            }],
        }],
    })

    query: Text = Field(description="Вопрос пользователя, до 4000 символов")
    history: list[HistoryMessage] = Field(
        default_factory=list, max_length=16,
        description="До 16 предыдущих сообщений; [] для первого вопроса",
    )
    sources: list[Source] = Field(
        max_length=32,
        description="До 32 фрагментов, найденных и разрешённых Go; общий контекст — до 64 КиБ",
    )

    @model_validator(mode="after")
    def validate_context(self):
        ids = [s.chunk_id for s in self.sources]
        if len(ids) != len(set(ids)) or any(s.int == 0 for s in ids):
            raise ValueError("Source IDs must be unique nonzero UUIDs")
        serialized = json.dumps(self.model_dump(mode="json"), ensure_ascii=False)
        if "\x00" in self.query or any("\x00" in s.content for s in self.sources):
            raise ValueError("NUL is not allowed")
        if len(serialized.encode("utf-8")) > 64 * 1024:
            raise ValueError("Combined query, history and sources exceed 64 KiB")
        return self


class GenerateResponse(StrictModel):
    status: Literal["answered", "needs_clarification", "insufficient_information"] = Field(
        description="answered — ответ готов; needs_clarification — нужно уточнение; insufficient_information — недостаточно информации",
    )
    answer: Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, max_length=12000)] | None = Field(
        description="Ответ нейронки; null при уточнении или отсутствии информации",
    )
    used_source_ids: list[UUID] = Field(max_length=32, description="UUID фрагментов, подтверждающих ответ")
    clarification_question: Text | None = Field(description="Уточняющий вопрос; null, если уточнение не требуется")

    @model_validator(mode="after")
    def validate_state(self):
        if len(self.used_source_ids) != len(set(self.used_source_ids)):
            raise ValueError("Duplicate citations")
        if self.status == "answered":
            if not self.answer or not self.used_source_ids or self.clarification_question is not None:
                raise ValueError("Answered requires an answer and citations only")
        elif self.status == "needs_clarification":
            if self.answer is not None or self.used_source_ids or not self.clarification_question:
                raise ValueError("Clarification requires a question only")
        elif self.answer is not None or self.used_source_ids or self.clarification_question is not None:
            raise ValueError("Insufficient information must not contain an answer or citations")
        for text in [self.answer, self.clarification_question]:
            if text and "\x00" in text:
                raise ValueError("NUL is not allowed")
        return self
