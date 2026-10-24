"""Выбор парсера по уже проверенному расширению файла."""

from pathlib import Path

from app.ingestion.parsers.docx import extract_docx
from app.ingestion.parsers.pdf import extract_pdf
from app.ingestion.parsers.txt import extract_txt

PARSERS = {".pdf": extract_pdf, ".docx": extract_docx, ".txt": extract_txt}


def extract_blocks(path: Path, suffix: str):
    return PARSERS[suffix](path)
