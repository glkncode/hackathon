"""Ошибки разбора, которые HTTP-слой преобразует в ответ клиенту."""


class IngestionError(Exception):
    def __init__(self, status_code: int, detail: str):
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail
