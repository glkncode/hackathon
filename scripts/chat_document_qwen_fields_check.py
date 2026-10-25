"""Actual Qwen -> Go -> v2 draft regression, isolated stack only.

Synthetic development scenarios; not a blind model evaluation. No prompt tuning.
"""
import json
import time
from copy import deepcopy

from chat_document_e2e import OUT, action, request, secrets_uuid, session, view


def main():
    fixtures = json.loads((OUT / "fixtures.json").read_text(encoding="utf-8"))
    fixture = next(f for f in fixtures["fixtures"] if f["module"] == "working-group")
    client = session(fixtures["username"], fixtures["password"])
    sid = request(client, "POST", "/api/v1/chat/sessions", 201,
                  json={"title": "Qwen natural field regression"}).json()["id"]
    action(client, sid, "choose", template_id=fixture["template_id"])
    current = view(client, sid)
    path = "/api/v1/template-drafts/" + current["draft"]["id"]
    values = deepcopy(fixture["values"])
    values["members"] = values["members"][:2]
    request(client, "PATCH", path, json=dict(expected_revision=1, values=values))
    results = []

    def turn(query, check):
        before = view(client, sid)
        start = time.monotonic()
        result = request(client, "POST", f"/api/v1/chat/sessions/{sid}/messages",
                         json=dict(content=query, client_message_id=secrets_uuid())).json()
        after = view(client, sid)
        passed = bool(check(before, after, result))
        results.append(dict(query=query, seconds=time.monotonic()-start, passed=passed,
                            response=result, before_revision=before["draft"]["revision"],
                            after_revision=after["draft"]["revision"], values=after["draft"]["values"]))
        print(("PASS " if passed else "FAIL ") + query, flush=True)

    action(client, sid, "edit", field_key="members")
    turn("Добавь третьего члена: должность — аналитик, подразделение — отдел ИТ, ФИО — Соколов Павел Игоревич. Первых двух сохрани без изменений.",
         lambda b, a, _: len(a["draft"]["values"]["members"]) == 3
         and a["draft"]["values"]["members"][:2] == b["draft"]["values"]["members"]
         and a["draft"]["values"]["members"][2]["name"] == "Соколов Павел Игоревич")
    turn("Во второй строке членов исправь ФИО на Волкова Ирина Олеговна. Должность, подразделение и остальные строки не меняй.",
         lambda b, a, _: a["draft"]["values"]["members"][1]["name"] == "Волкова Ирина Олеговна"
         and a["draft"]["values"]["members"][0] == b["draft"]["values"]["members"][0]
         and a["draft"]["values"]["members"][2:] == b["draft"]["values"]["members"][2:]
         and a["draft"]["values"]["members"][1]["position"] == b["draft"]["values"]["members"][1]["position"])
    turn("Удали третьего члена группы из списка, первых двух оставь.",
         lambda b, a, _: a["draft"]["values"]["members"] == b["draft"]["values"]["members"][:2])
    action(client, sid, "edit", field_key="deadline")
    turn("Пока не знаю, какую дату поставить.",
         lambda b, a, _: a["draft"]["revision"] == b["draft"]["revision"])
    turn("Измени конечный срок на 20 декабря 2026 года, а итоговый документ на отчёт об обследовании.",
         lambda b, a, _: a["draft"]["values"]["deadline"] == "2026-12-20"
         and a["draft"]["values"]["result"] == "отчёт об обследовании")
    turn("Кто согласовывает распоряжение о создании рабочей группы?",
         lambda b, a, r: r.get("mode") != "document" and a["draft"]["revision"] == b["draft"]["revision"])
    turn("Отложим заполнение документа на потом.", lambda b, a, _: a["workflow"]["stage"] == "paused")
    turn("Продолжим заполнение документа.", lambda b, a, _: a["workflow"]["stage"] == "reviewing")
    turn("Второго назначь как в прошлый раз, фамилию не помню.",
         lambda b, a, _: a["draft"]["revision"] == b["draft"]["revision"])
    (OUT / "qwen-fields.json").write_text(json.dumps(dict(session_id=sid, results=results),
                                                     ensure_ascii=False, indent=2), encoding="utf-8")
    assert all(r["passed"] for r in results), "Natural-language regression failed; inspect qwen-fields.json"


if __name__ == "__main__":
    main()
