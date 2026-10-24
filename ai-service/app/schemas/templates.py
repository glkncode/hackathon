"""Схемы результата проверки DOCX-шаблона."""

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class TemplateField(BaseModel):
    model_config = ConfigDict(extra="forbid")
    name: str = Field(description="Имя поля шаблона, например recipient")
    type: Literal["string"] = Field(default="string", description="Тип значения: строка")
    required: bool = Field(default=True, description="Нужно ли заполнить поле перед созданием документа")


class Inspection(BaseModel):
    model_config = ConfigDict(extra="forbid")
    fields: list[TemplateField] = Field(description="Все обнаруженные поля шаблона")
    required_fields: list[str] = Field(description="Имена обязательных полей")
    missing_fields: list[str] = Field(description="Поля, для которых не переданы непустые значения")
    can_render: bool = Field(description="Можно ли создать документ с переданными значениями")
    validation: dict | None = Field(default=None, description="Версионированный отчёт Profile 2.0")


class Binding(BaseModel):
    model_config = ConfigDict(extra="forbid")
    part: str = Field(default="word/document.xml", pattern=r"^word/(document|header[0-9]+|footer[0-9]+|footnotes)\.xml$")
    paragraph: int = Field(ge=0, le=100000)
    match: str = Field(min_length=1, max_length=8000)
    suffix: str = Field(default="", max_length=128)


class FieldDefinition(BaseModel):
    model_config = ConfigDict(extra="forbid")
    key: str = Field(pattern=r"^[a-z][a-z0-9_]{0,63}$")
    label: str = Field(min_length=1, max_length=255)
    type: Literal["text", "multiline_text", "person", "date", "person_list"]
    required: bool
    question: str = Field(min_length=1, max_length=1000)
    description: str = Field(default="", max_length=2000)
    max_length: int = Field(default=8000, ge=1, le=8000)
    bindings: list[Binding] = Field(default_factory=list, max_length=16)
