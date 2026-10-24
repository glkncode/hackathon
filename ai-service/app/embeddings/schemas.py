from typing import Annotated, Literal

from pydantic import Field, StringConstraints, model_validator

from app.schemas.rag import StrictModel


class EmbeddingRequest(StrictModel):
    kind: Literal['query', 'passage']
    inputs: list[Annotated[str, StringConstraints(strict=True, strip_whitespace=True,
                                                min_length=1, max_length=66048)]] = Field(min_length=1, max_length=8)

    @model_validator(mode='after')
    def bounded(self):
        if sum(len(s.encode('utf-8')) for s in self.inputs) > 256 * 1024:
            raise ValueError('Embedding input exceeds byte budget')
        return self


class Window(StrictModel):
    input_index: int = Field(ge=0, strict=True)
    window_index: int = Field(ge=0, strict=True)
    start_token: int = Field(ge=0, strict=True)
    end_token: int = Field(gt=0, strict=True)
    total_content_tokens: int = Field(gt=0, strict=True)


class EmbeddingResponse(StrictModel):
    model: str
    revision: str
    dimensions: Literal[384]
    format_revision: str
    embeddings: list[list[float]]
    windows: list[Window]
