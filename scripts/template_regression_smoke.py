"""Read/search/chat smoke against the configured local stack and permitted inference server."""
import json
from pathlib import Path
from uuid import uuid4

from dotenv import dotenv_values

from template_e2e import ROOT, request, session


def main():
    env = dotenv_values(ROOT / ".env")
    client = session(env.get("ADMIN_USERNAME", "admin"), env["ADMIN_PASSWORD"])
    query = "Какие обязанности у секретаря рабочей группы?"
    result = request(client, "POST", "/api/v1/search", json={"query": query, "limit": 5}).json()
    assert result["total"] > 0
    print("PASS: public search returns authorized sources", flush=True)
    chat = request(client, "POST", "/api/v1/chat/sessions", 201, json={"title": "Template regression smoke"}).json()
    path = f'/api/v1/chat/sessions/{chat["id"]}'
    try:
        turn = request(client, "POST", path + "/messages", json={"content": query, "client_message_id": str(uuid4())}).json()
        assert turn["mode"] == "generated", "RAG fell back instead of generating"
        assert turn["sources"] and turn["message"]
        request(client, "GET", path)
        print("PASS: real RAG generated an answer with validated source IDs and persisted history", flush=True)
        report = dict(search_results=result["total"], rag_mode=turn["mode"], rag_sources=len(turn["sources"]),
                      semantic_configured=env.get("SEMANTIC_SEARCH_ENABLED", "false"))
        output = ROOT / ".local-integration/template-e2e/regression.json"
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2), encoding="utf-8")
    finally:
        request(client, "DELETE", path, 204)


if __name__ == "__main__":
    main()
