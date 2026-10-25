"""Conversational orchestration against isolated Go/Postgres/Python/React stack.

Run ai-service/.venv/Scripts/python.exe scripts/chat_document_e2e.py.
Never selects the primary database or port. Synthetic examples are development
tests, not an external blind test. Browser verification is a separate procedure.
"""
import hashlib
import io
import json
import secrets
import statistics
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from zipfile import ZipFile

from lxml import etree
import template_e2e as api
from template_v2_cases import cases, source_path
from template_v2_e2e import verify

api.BASE = "http://localhost:18080"
OUT = api.ROOT / ".local-integration/chat-documents"
request, session = api.request, api.session


def view(client, sid):
    return request(client, "GET", f"/api/v1/chat/sessions/{sid}/document").json()


def action(client, sid, name, expected=200, payload=None, **extra):
    if payload is None:
        v = view(client, sid)
        payload = dict(action=name, client_message_id=secrets_uuid(),
                       workflow_version=v.get("workflow", {}).get("version", 0),
                       expected_revision=v.get("draft", {}).get("revision", 0), **extra)
    return request(client, "POST", f"/api/v1/chat/sessions/{sid}/document/actions", expected, json=payload).json()


def secrets_uuid():
    from uuid import uuid4
    return str(uuid4())


def structure(content):
    with ZipFile(io.BytesIO(content)) as z:
        return {name: etree.tostring(etree.fromstring(z.read(name)), method="c14n")
                for name in z.namelist() if name.startswith("word/") and name.endswith(".xml")}


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    api.health()
    admin = session("chat_admin", "isolated-chat-admin-test-only")
    suffix = secrets.token_hex(4)
    password = "Synthetic-chat-" + secrets.token_hex(12)
    username = "chat_" + suffix
    user = request(admin, "POST", "/api/v1/users", 201, json=dict(username=username, password=password)).json()
    client = session(username, password)
    otherpass = "Synthetic-" + secrets.token_hex(12)
    other = request(admin, "POST", "/api/v1/users", 201, json=dict(username="other_" + suffix, password=otherpass)).json()
    foreign = session(other["username"], otherpass)
    reports, fixtures, latencies = [], [], []
    for module, profile, values in cases():
        doc = request(admin, "POST", "/api/v1/documents", 201,
                      data={"title": "Chat E2E " + module + " " + suffix},
                      files={"file": (source_path(profile).name, source_path(profile).read_bytes(), api.MIME)}).json()
        template = request(admin, "POST", "/api/v1/templates", 201, json=dict(
            document_id=doc["id"], document_version_id=doc["latest_version"]["id"],
            name=profile.get("title", profile["field_source"]["name"]).replace(".docx", ""),
            description="Зарегистрированный модуль: " + module, profile=profile)).json()
        access = f'/api/v1/documents/{doc["id"]}/access'
        request(admin, "POST", access, 201, json={"user_id": user["id"]})
        sid = request(client, "POST", "/api/v1/chat/sessions", 201, json={"title": "Chat " + module}).json()["id"]
        action(client, sid, "start")
        before = view(client, sid)
        payload = dict(action="choose", template_id=template["id"], client_message_id=secrets_uuid(),
                       workflow_version=before["workflow"]["version"], expected_revision=0)
        first = action(client, sid, "choose", payload=payload)
        replay = action(client, sid, "choose", payload=payload)
        assert first["assistant_message"]["id"] == replay["assistant_message"]["id"]
        changed = dict(payload, template_id=secrets_uuid())
        action(client, sid, "choose", 409, payload=changed)
        draft = view(client, sid)["draft"]
        path = f'/api/v1/template-drafts/{draft["id"]}'
        request(foreign, "GET", f"/api/v1/chat/sessions/{sid}/document", 404)
        action(foreign, sid, "start", 404, payload=dict(action="start", client_message_id=secrets_uuid()))
        blocked = action(client, sid, "generate")
        assert "нельзя" in blocked["assistant_message"]["content"]
        # All active required questions come from the same validation as Wizard.
        steps = 0
        while True:
            current = view(client, sid)
            validation = current["draft"]["validation"]
            if validation["ready"]:
                break
            question = validation["next_question"]
            assert question is not None, validation
            key = question["key"]
            start = time.monotonic()
            action(client, sid, "answer", values={key: values[key]})
            latencies.append(time.monotonic() - start)
            steps += 1
            assert steps < 65, "question did not advance"
        # Populate optional metadata through the same allowed active schema.
        current = view(client, sid)
        active = current["draft"]["validation"].get("visible_fields", [])
        optional = {k: v for k, v in values.items() if k in active and current["draft"]["values"].get(k) != v}
        if optional:
            action(client, sid, "answer", values=optional)
        # Invalid keys/types/dates never reach stored draft.
        current = view(client, sid)
        action(client, sid, "answer", 400, values={"invented_key": "x"})
        date = next((f for f in current["draft"]["fields"] if f["type"] == "date" and f["key"] in active), None)
        if date:
            invalid = action(client, sid, "answer", values={date["key"]: "2026-02-30"})
            assert "не принят" in invalid["assistant_message"]["content"]
            assert view(client, sid)["draft"]["revision"] == current["draft"]["revision"]
        action(client, sid, "pause")
        assert view(client, sid)["workflow"]["stage"] == "paused"
        action(client, sid, "resume")
        # Flip each active choice through its declared alternatives. The Chat
        # projection must match the original Wizard validation at every branch.
        branches = 0
        branch_base = view(client, sid)["draft"]
        for field in branch_base["fields"]:
            if field["type"] != "choice" or field["key"] not in branch_base["validation"]["visible_fields"]:
                continue
            key = field["key"]
            original_value = branch_base["values"].get(key)
            for option in field["options"]:
                if option == original_value:
                    continue
                action(client, sid, "answer", values={key: option})
                branch = view(client, sid)["draft"]
                assert branch["validation"] == request(client, "POST", path + "/validate").json()
                assert branch["values"].get(key) == option
                branches += 1
            action(client, sid, "answer", values={key: original_value})
        restored = view(client, sid)["draft"]
        assert restored["values"] == branch_base["values"]
        # Repeating edits use a full validated replacement and a revision CAS.
        repeat = next((f for f in current["draft"]["fields"] if f["type"] == "repeater"), None)
        if repeat:
            key = repeat["key"]
            original = deepcopy(view(client, sid)["draft"]["values"][key])
            rows = deepcopy(original)
            rows[1]["name"] = "Изменённый тестовый участник"
            action(client, sid, "answer", values={key: rows})
            action(client, sid, "answer", values={key: rows[:2]})
            action(client, sid, "answer", values={key: original})
        # Two tabs: one update wins, stale second tab is rejected.
        current = view(client, sid)
        key = next(f["key"] for f in current["draft"]["fields"] if f["type"] == "text" and f["key"] in active)
        stale = dict(action="answer", client_message_id=secrets_uuid(), expected_revision=current["draft"]["revision"],
                     workflow_version=current["workflow"]["version"], values={key: current["draft"]["values"][key]})
        action(client, sid, "answer", values=stale["values"])
        action(client, sid, "answer", 409, payload=stale)
        # Ordinary Wizard updates the actual same draft. New confirmation required.
        current = view(client, sid)
        wizard = request(client, "PATCH", path, json=dict(expected_revision=current["draft"]["revision"], values={key: current["draft"]["values"][key]})).json()
        refused = action(client, sid, "generate")
        assert "подтвердите" in refused["assistant_message"]["content"]
        action(client, sid, "validate")
        current = view(client, sid)
        assert current["draft"]["validation"]["ready"]
        payload = dict(action="generate", client_message_id=secrets_uuid(), expected_revision=current["draft"]["revision"], workflow_version=current["workflow"]["version"])
        with ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda _: action(client, sid, "generate", payload=payload), range(2)))
        assert results[0]["assistant_message"]["id"] == results[1]["assistant_message"]["id"]
        result = view(client, sid)
        artifact = result["generated"]
        generated_id = artifact["id"]
        assert results[0]["assistant_message"]["document_generated_id"] == generated_id
        output = request(client, "GET", f"/api/v1/generated-documents/{generated_id}/download").content
        verify(profile, result["draft"]["values"], output)
        (OUT / (module + ".docx")).write_bytes(output)
        # Equivalence: same values, separate ordinary Wizard draft, same content.
        wizard_draft = request(client, "POST", f'/api/v1/templates/{template["id"]}/drafts', 201).json()
        wizard_path = f'/api/v1/template-drafts/{wizard_draft["id"]}'
        wizard_draft = request(client, "PATCH", wizard_path, json=dict(expected_revision=1, values=result["draft"]["values"])).json()
        assert wizard_draft["values"] == result["draft"]["values"]
        assert wizard_draft["validation"] == result["draft"]["validation"]
        wg = request(client, "POST", wizard_path + "/generate", json=dict(expected_revision=wizard_draft["revision"])).json()
        assert structure(request(client, "GET", wg["download_url"]).content) == structure(output)
        request(foreign, "GET", f"/api/v1/generated-documents/{generated_id}/download", 404)
        request(client, "GET", "/api/v1/generated-documents/" + secrets_uuid(), 404)
        action(client, sid, "generate")
        assert view(client, sid)["generated"]["id"] == generated_id
        # Revocation hides both stored field replies and the artifact identifier.
        request(admin, "DELETE", access + "/" + user["id"], 204)
        assert view(client, sid)["unavailable"]
        history = request(client, "GET", f"/api/v1/chat/sessions/{sid}?limit=100&offset=0").json()
        assert all(not m.get("document_generated_id") for m in history["messages"])
        assert all("Изменённый тестовый участник" not in m["content"] for m in history["messages"])
        request(client, "GET", f"/api/v1/generated-documents/{generated_id}/download", 404)
        request(admin, "POST", access, 201, json={"user_id": user["id"]})
        action(client, sid, "cancel")
        assert request(client, "GET", path).json()["id"] == draft["id"]
        fixtures.append(dict(module=module, template_id=template["id"], profile=profile, values=values, chat_session_id=sid, draft_id=draft["id"], artifact_id=generated_id))
        reports.append(dict(module=module, status="passed", questions=steps, conditional_choices=branches, generated_id=generated_id, sha256=hashlib.sha256(output).hexdigest(), wizard_equivalence=True))
        print(f"PASS {module}: {steps} questions, real DOCX, Wizard equivalence, replay/CAS/ACL", flush=True)
    # CSRF remains mandatory for document actions.
    request(client, "POST", f'/api/v1/chat/sessions/{fixtures[0]["chat_session_id"]}/document/actions', 403,
            csrf=False, json=dict(action="start", client_message_id=secrets_uuid()))
    metrics = dict(samples=len(latencies), p50_seconds=statistics.median(latencies), p95_seconds=sorted(latencies)[int(len(latencies)*.95)-1])
    (OUT / "e2e.json").write_text(json.dumps(dict(profiles=reports, deterministic_latency=metrics), ensure_ascii=False, indent=2), encoding="utf-8")
    (OUT / "fixtures.json").write_text(json.dumps(dict(username=username, password=password, fixtures=fixtures), ensure_ascii=False, indent=2), encoding="utf-8")
    print("PASS seven profiles; deterministic latency:", metrics)


if __name__ == "__main__":
    main()
