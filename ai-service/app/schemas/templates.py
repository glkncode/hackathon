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
