"""Извлечение абзацев и таблиц DOCX в исходном порядке."""

from pathlib import Path
from zipfile import ZipFile

from docx import Document

from app.ingestion.errors import IngestionError


def extract_docx(path: Path):
    # Проверяем объём распаковки до загрузки XML библиотекой python-docx.
    with ZipFile(path) as archive:
        entries = archive.infolist()
        if len(entries) > 10000 or sum(e.file_size for e in entries) > 64 * 1024 * 1024:
            raise IngestionError(422, "DOCX decompressed size exceeds limit")
    document = Document(path)
    # У DOCX нет надёжной нумерации страниц, поэтому возвращаем None.
    for block in document.iter_inner_content():
        if hasattr(block, "rows"):
            for row in block.rows:
                yield None, "\t".join(cell.text for cell in row.cells)
        else:
            yield None, block.text
