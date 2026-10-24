"""Чтение UTF-8 TXT, включая файлы с BOM."""

from pathlib import Path


def extract_txt(path: Path):
    yield None, path.read_text(encoding="utf-8-sig")
