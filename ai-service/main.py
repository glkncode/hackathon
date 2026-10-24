"""Точка входа Uvicorn: python -m uvicorn main:app."""

from app.application import create_app

app = create_app()