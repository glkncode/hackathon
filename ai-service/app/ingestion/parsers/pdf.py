"""Извлечение текста PDF с номерами страниц; OCR здесь не выполняется."""

from pathlib import Path

import pymupdf

from app.ingestion.errors import IngestionError


def extract_pdf(path: Path):
    # Открытие из памяти не оставляет блокировку повреждённого файла в Windows.
    with pymupdf.open(stream=path.read_bytes(), filetype="pdf") as document:
        if not document.is_pdf or document.needs_pass:
            raise ValueError("Invalid or encrypted PDF")
        if len(document) > 10000:
            raise IngestionError(422, "PDF exceeds 10000 pages")
        for index, page in enumerate(document, start=1):
            yield index, page.get_text()
