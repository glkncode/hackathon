"""Проверка независимых экземпляров приложения после разделения на модули."""

from uuid import uuid4

from fastapi.testclient import TestClient

from app.application import create_app


def test_app_instances_use_their_own_state(monkeypatch):
    monkeypatch.delenv("LLM_BASE_URL", raising=False)
    monkeypatch.setenv("AI_SERVICE_TOKEN", "first-token")
    with TestClient(create_app()) as first:
        monkeypatch.setenv("AI_SERVICE_TOKEN", "second-token")
        with TestClient(create_app()) as second:
            for client, token in ((first, "first-token"), (second, "second-token")):
                # Ручки должны брать ресурсы из request.app, без глобального приложения.
                response = client.post(
                    "/internal/v1/ingest",
                    headers={"X-Service-Token": token},
                    data={
                        "document_id": str(uuid4()),
                        "document_version_id": str(uuid4()),
                        "mime_type": "text/plain",
                    },
                    files={"file": ("sample.txt", b"isolated application")},
                )
                assert response.status_code == 200
                assert response.json()["chunks"][0]["content"] == "isolated application"
                response = client.post(
                    "/internal/v1/rag/generate",
                    headers={"X-Service-Token": token},
                    json={"query": "Вопрос", "sources": []},
                )
                assert response.status_code == 200
                assert response.json()["status"] == "insufficient_information"
            assert second.post(
                "/internal/v1/ingest", headers={"X-Service-Token": "first-token"}
            ).status_code == 401
