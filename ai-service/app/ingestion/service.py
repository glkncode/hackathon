"""Временное сохранение файла и полный цикл его разбора."""

from pathlib import Path
from tempfile import TemporaryDirectory
from typing import BinaryIO

from app.chunking.text import make_chunks
from app.core.config import MAX_FILE_BYTES
from app.ingestion.errors import IngestionError
from app.ingestion.parsers import extract_blocks


def save_and_parse(source: BinaryIO, suffix: str) -> dict:
    # Отдельная папка каждого запроса удаляется и при успешном разборе, и при ошибке.
    with TemporaryDirectory(prefix="document-ingest-") as directory:
        path = Path(directory) / f"document{suffix}"
        size = 0
        with path.open("wb") as destination:
            while chunk := source.read(1024 * 1024):
                size += len(chunk)
                if size > MAX_FILE_BYTES:
                    raise IngestionError(413, "File exceeds 20 MiB")
                destination.write(chunk)
        if not size:
            raise IngestionError(422, "Empty file")
        try:
            chunks = make_chunks(extract_blocks(path, suffix))
        except IngestionError:
            raise
        except Exception as exc:
            raise IngestionError(422, "Cannot read document; expected PDF, DOCX or UTF-8 TXT") from exc
        return {"chunks": chunks, "warnings": []}
