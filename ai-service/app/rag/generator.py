import json

from pydantic import ValidationError

from app.llm.client import InferenceError, LLMClient
from app.rag.prompts import messages_for
from app.rag.schemas import GenerateRequest, GenerateResponse


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON key")
        result[key] = value
    return result


async def generate(request: GenerateRequest, client: LLMClient) -> GenerateResponse:
    if not request.sources:
        return GenerateResponse(
            status="insufficient_information", answer=None,
            used_source_ids=[], clarification_question=None,
        )
    raw = await client.generate(messages_for(request))
    try:
        body = json.loads(raw, object_pairs_hook=unique_object)
        result = GenerateResponse.model_validate(body)
        allowed = {source.chunk_id for source in request.sources}
        if not set(result.used_source_ids).issubset(allowed):
            raise ValueError("Unknown source IDs")
        return result
    except (ValueError, TypeError, ValidationError):
        # Never include inference output, questions or documents in errors/logs.
        raise InferenceError("llm_invalid_response", 502) from None
