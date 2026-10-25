"""Frozen internal regression prompts, actual configured Qwen, no prompt tuning.

This is a Codex-created internal holdout, never an external blind evaluation.
"""
import json
import statistics
import time
import httpx
from chat_document_e2e import OUT, session, request


def main():
    fixtures = json.loads((OUT / "fixtures.json").read_text(encoding="utf-8"))
    user = session(fixtures["username"], fixtures["password"])
    ids = {f["module"]: f["template_id"] for f in fixtures["fixtures"]}
    catalogue = request(user, "GET", "/api/v1/templates?limit=100&offset=0").json()["items"]
    templates = [dict(id=t["id"], name=t["name"], description=t["description"]) for t in catalogue]
    cases = json.loads((OUT.parents[1] / "scripts/chat_document_qwen_cases.json").read_text(encoding="utf-8"))
    results = []
    with httpx.Client(base_url="http://localhost:18001", timeout=65, trust_env=False) as client:
        for group, tests in cases.items():
            for test in tests:
                start = time.monotonic()
                response = client.post("/internal/v1/chat-documents/interpret", headers={"X-Service-Token":"isolated-chat-service-test-only"}, json=dict(
                    query=test["query"], stage="", templates=templates, fields=[], values={}, expected_field=""))
                elapsed = time.monotonic() - start
                plan = response.json()
                ok = response.status_code == 200 and plan.get("intent") == test["intent"]
                if "module" in test:
                    ok = ok and plan.get("template_ids") == [ids[test["module"]]]
                results.append(dict(group=group, query=test["query"], expected=test, response=plan, seconds=elapsed, passed=ok))
                print(("PASS" if ok else "FAIL") + " " + group + ": " + test["query"], flush=True)
    times = sorted(r["seconds"] for r in results)
    metrics = dict(samples=len(times), p50_seconds=statistics.median(times), p95_seconds=times[max(0, int(len(times)*.95)-1)])
    (OUT / "qwen-check.json").write_text(json.dumps(dict(results=results, latency=metrics), ensure_ascii=False, indent=2), encoding="utf-8")
    print(metrics)
    assert all(r["passed"] for r in results)


if __name__ == "__main__":
    main()
