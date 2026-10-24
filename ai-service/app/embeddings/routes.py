import asyncio
import os

from fastapi import APIRouter
from starlette.responses import JSONResponse

from app.embeddings.model import engine, EmbeddingUnavailable
from app.embeddings.schemas import EmbeddingRequest, EmbeddingResponse

router = APIRouter()


@router.post('/internal/v1/embeddings', response_model=EmbeddingResponse)
async def embeddings(body: EmbeddingRequest):
    # No waiting queue: a timed-out ONNX call keeps its slot until the CPU
    # operation actually finishes, preventing cancellation from multiplying RAM.
    if not engine.lock.acquire(blocking=False):
        return JSONResponse({'error': {'code': 'embedding_busy'}}, status_code=503)

    def run():
        try:
            return engine.encode(body)
        finally:
            engine.lock.release()

    task = asyncio.create_task(asyncio.to_thread(run))
    # Consume exceptions even if the HTTP timeout expires before the CPU call.
    task.add_done_callback(lambda t: t.exception() if not t.cancelled() else None)
    try:
        return await asyncio.wait_for(asyncio.shield(task), timeout=float(os.environ.get('EMBEDDING_TIMEOUT_SECONDS', '30')))
    except TimeoutError:
        return JSONResponse({'error': {'code': 'embedding_timeout'}}, status_code=504)
    except ValueError:
        return JSONResponse({'error': {'code': 'embedding_input_limit'}}, status_code=422)
    except Exception:
        return JSONResponse({'error': {'code': 'embedding_unavailable'}}, status_code=503)
