"""Opt-in Qwen evaluation on a private local export of existing source rows.

No retrieval, database writes, service changes, or request/response logging.
Raw model outputs are saved only in the explicitly supplied local report file.
The checks below are benchmark expectations, never production answer rules.
"""

import argparse
import asyncio
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import time
from unittest.mock import AsyncMock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.core.config import LLMConfig
from app.llm.client import InferenceError, LLMClient
from app.rag import prompts
from app.rag.generator import generate
from app.schemas.rag import GenerateRequest


SCENARIOS = [
    ("composition", "Состав рабочей группы", [10, 11, 12, 13, 14], "answered"),
    ("membership", "Кто входит в состав рабочей группы?", [10, 11, 12, 13, 14], "answered"),
    ("leader_duties", "Какие обязанности у руководителя рабочей группы?", [16], "answered"),
    ("secretary_duties", "За что отвечает секретарь рабочей группы?", [17], "answered"),
    ("control", "Кто контролирует исполнение распоряжения?", [19], "answered"),
    ("leader_name", "Как зовут руководителя группы?", [10, 11, 12, 13, 14], "answered"),
    ("blank_deadline", "Какой конкретный срок выполнения работы группы?", [15], "answered"),
    ("ambiguous_deadline", "Какой срок?", [15, 16, 18], "needs_clarification"),
    ("unsupported", "Каков размер ежемесячной премии секретаря?", [17], "insufficient_information"),
    ("suggested_controller", "Иванов контролирует исполнение распоряжения?", [19, 20, 30], "answered"),
    ("blank_date", "Какая дата утверждения распоряжения?", [0], "answered"),
    ("blank_number", "Какой регистрационный номер распоряжения?", [5], "answered"),
]


def check(case, result, sources):
    """Conservative smoke checks; semantic grounding also needs human review."""
    name, _, _, expected = case
    errors = []
    if result["status"] != expected:
        errors.append("status")
    ids = result["used_source_ids"]
    allowed = {s["chunk_id"] for s in sources}
    if not set(ids).issubset(allowed):
        errors.append("unknown_source_ids")
    if name == "ambiguous_deadline" and result["status"] == "needs_clarification":
        question = result["clarification_question"].lower().replace("ё", "е")
        context = " ".join(s["content"] for s in sources).lower().replace("ё", "е")
        if "отчет" in question and "отчет" not in context:
            errors.append("invented_result_type_in_clarification")
    if expected != "answered" or result["status"] != "answered":
        return errors
    answer = result["answer"].lower()
    unavailable = any(term in answer for term in (
        "не указан", "не заполн", "не определ", "отсутств", "не содержит", "не привед",
    ))
    if name in {"composition", "membership"}:
        if not all(term in answer for term in ("руководител", "секретар", "член")) or not unavailable:
            errors.append("roles_or_blank_fields")
        role_ids = {s["chunk_id"] for s in sources if s["chunk_index"] in {12, 13, 14}}
        if not role_ids.issubset(ids):
            errors.append("role_citations")
    elif name == "leader_duties":
        if not all(term in answer for term in ("организ", "распредел", "срок")):
            errors.append("duties")
    elif name == "secretary_duties":
        if not all(term in answer for term in ("оформ", "заседан", "хран", "материал")):
            errors.append("duties")
    elif not unavailable:
        errors.append("missing_blank_field_explanation")
    if name in {"control", "suggested_controller"} and not any(t in answer for t in ("контрол", "исполнен")):
        errors.append("control_clause")
    # A blank template must not acquire concrete dates or registration numbers.
    if re.search(r"\b\d{1,2}[./-]\d{1,2}[./-]\d{2,4}\b|\b20\d{2}\b|№\s*\d+", answer):
        errors.append("invented_requisite")
    return errors


class RecordingClient(LLMClient):
    async def generate(self, messages):
        self.raw = None
        self.raw = await super().generate(messages)
        return self.raw


async def evaluate(args):
    rows = json.loads(args.sources.read_text(encoding="utf-8"))
    by_index = {row["chunk_index"]: row for row in rows}
    if args.prompt_file:
        spec = importlib.util.spec_from_file_location("evaluation_prompt", args.prompt_file)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        prompts.SYSTEM_PROMPT = module.SYSTEM_PROMPT
    config = LLMConfig.from_env()
    if config.model != "qwen3.6-35b-a3b":
        raise SystemExit("Expected qwen3.6-35b-a3b; refusing to evaluate a different model")
    report = {
        "model": config.model,
        "parameters": {"temperature": 0, "max_tokens": 4096, "stream": False,
                       "response_format": {"type": "json_object"}},
        "prompt_sha256": hashlib.sha256(prompts.SYSTEM_PROMPT.encode()).hexdigest(),
        "sources_sha256": hashlib.sha256(args.sources.read_bytes()).hexdigest(),
        "repeats": args.repeats, "runs": [],
    }
    client = RecordingClient(config)
    cases = SCENARIOS[:5] if args.core_only else SCENARIOS
    if args.cases:
        cases = [case for case in cases if case[0] in args.cases]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    try:
        for case in cases:
            name, query, indices, expected = case
            sources = [by_index[index] for index in indices]
            request = GenerateRequest.model_validate({
                "query": query, "history": [],
                "sources": [{k: v for k, v in row.items() if k != "chunk_index"} for row in sources],
            })
            for repeat in range(args.repeats):
                run = {"case": name, "repeat": repeat + 1, "expected_status": expected,
                       "source_ids": [s["chunk_id"] for s in sources]}
                started = time.monotonic()
                try:
                    result = (await generate(request, client)).model_dump(mode="json")
                    run["result"] = result
                    run["checks_failed"] = check(case, result, sources)
                    # Replay the exact raw string through production validation, without
                    # a second inference. Detect any postprocessing status conversion.
                    replay = await generate(request, AsyncMock(generate=AsyncMock(return_value=client.raw)))
                    raw_status = json.loads(client.raw)["status"]
                    run["raw_status"] = raw_status
                    run["postprocessing_preserved"] = (
                        replay.model_dump(mode="json") == result and raw_status == result["status"]
                    )
                except InferenceError as exc:
                    run["error"] = exc.code
                    run["checks_failed"] = [exc.code]
                run["raw"] = client.raw
                run["seconds"] = round(time.monotonic() - started, 3)
                report["runs"].append(run)
                args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            runs = [r for r in report["runs"] if r["case"] == name]
            print(json.dumps({"case": name, "statuses": dict(Counter(
                r.get("result", {}).get("status", r.get("error")) for r in runs)),
                "checks_passed": sum(not r["checks_failed"] for r in runs), "runs": len(runs),
                "postprocessing_preserved": all(r.get("postprocessing_preserved") for r in runs)}), flush=True)
    finally:
        await client.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="Private local report; do not publish raw outputs")
    parser.add_argument("--env-file", type=Path, help="Explicit local LLM configuration (never printed)")
    parser.add_argument("--prompt-file", type=Path, help="Optional saved baseline prompts.py")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--core-only", action="store_true")
    selection.add_argument("--case", dest="cases", action="append",
                           choices=[case[0] for case in SCENARIOS])
    parser.add_argument("--repeats", type=int, default=5)
    args = parser.parse_args()
    if args.repeats < 5:
        parser.error("At least five repeats are required")
    if args.env_file:
        from dotenv import dotenv_values
        for key, value in dotenv_values(args.env_file).items():
            if key.startswith("LLM_") and value is not None:
                os.environ[key] = value
    asyncio.run(evaluate(args))


if __name__ == "__main__":
    main()
