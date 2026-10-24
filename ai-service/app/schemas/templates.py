"""Схемы результата проверки DOCX-шаблона."""

from typing import Literal

from pydantic import BaseModel, ConfigDict


class TemplateField(BaseModel):
    model_config = ConfigDict(extra="forbid")
    name: str
    type: Literal["string"] = "string"
    required: bool = True


class Inspection(BaseModel):
    model_config = ConfigDict(extra="forbid")
    fields: list[TemplateField]
    required_fields: list[str]
    missing_fields: list[str]
    can_render: bool
