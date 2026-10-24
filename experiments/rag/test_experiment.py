import json
import os
from pathlib import Path

import numpy as np
import pytest

from embedding import DIMENSION, E5, cosine_candidates, metrics, rrf, token_windows


def test_windows_cover_every_token_including_tail():
    ids = list(range(1601))
    windows = list(token_windows(ids, 508))
    assert {token for _, _, window in windows for token in window} == set(ids)
    assert windows[-1][1] == len(ids)
    assert all(len(window) + 4 <= 512 for _, _, window in windows)
    assert windows[1][0] == windows[0][1] - 64


def test_max_window_cosine_preserves_real_chunk_id_and_deduplicates():
    query = np.eye(1, DIMENSION, dtype=np.float32)
    vectors = np.vstack([query, -query, query * 0.5])
    result = cosine_candidates(query, vectors, ["chunk-a", "chunk-a", "chunk-b"])
    assert result == [("chunk-a", 1.0), ("chunk-b", 0.5)]
    with pytest.raises(ValueError):
        cosine_candidates(query[:, :10], vectors, ["a", "a", "b"])


def test_rrf_score_scales_cannot_dominate_or_invent_ids():
    fused = rrf([[("a", 99999), ("b", 2)], [("b", 0.8), ("c", 0.7)]])
    assert [sid for sid, _ in fused] == ["b", "a", "c"]
    assert set(sid for sid, _ in fused) == {"a", "b", "c"}
    with pytest.raises(ValueError):
        rrf([[('a', 1), ('a', 0.1)]])


def test_metrics_measure_all_gold_chunks_and_negative_evidence_separately():
    rows = [
        {"gold_chunk_ids": ["a", "b"], "results": [("a", 1), ("x", 0)], "latency_ms": 1},
        {"gold_chunk_ids": [], "results": [("x", 1)], "latency_ms": 2},
    ]
    result = metrics(rows)
    assert result["recall_at_5"] == 0.5
    assert result["mrr"] == 1
    assert result["candidate_precision"] == pytest.approx(1 / 3)
    assert result["negative_candidate_rate"] == 1


def test_corpus_has_disjoint_documents_frozen_labels_and_varied_tasks():
    corpus = json.loads((Path(__file__).parent / "corpus.json").read_text(encoding="utf-8"))
    docs = corpus["documents"]
    assert len([d for d in docs if not d.get("denied")]) >= 8
    assert len({d["key"] for d in docs}) == len(docs)
    assert {d["format"] for d in docs} == {"docx", "txt", "pdf"}
    questions = [q for d in docs for q in d["questions"]] + corpus["negatives"]
    assert len({q["id"] for q in questions}) == len(questions)
    assert {"followup", "compound", "negation", "multi_document", "contradiction", "acl"}.issubset(
        {q.get("kind") for q in questions})


@pytest.fixture(scope="module")
def model():
    path = os.environ.get("RAG_EXPERIMENT_MODEL_PATH")
    if not path:
        pytest.skip("Explicit local model path required; tests never download models")
    return E5(Path(path))


@pytest.mark.parametrize("kind", ["query", "passage"])
@pytest.mark.parametrize("text", ["Проверка русскоязычного фрагмента.", "hello world", "Казахстан\t1800 рублей"])
def test_real_e5_prefix_is_equivalent_to_complete_formatted_text(model, kind, text):
    prefix = model.tokenizer.encode(kind + ":", add_special_tokens=False).ids
    content = model.tokenizer.encode(text, add_special_tokens=False).ids
    actual = [model.bos, *prefix, *content, model.eos]
    assert actual == model.tokenizer.encode(kind + ": " + text).ids


def test_real_model_long_passage_preserves_tail_and_rejects_overlong_query(model):
    text = "Проверка журнала технического обслуживания. " * 250
    vectors, spans = model.encode(text, "passage")
    assert len(vectors) > 1
    assert vectors.shape[1] == DIMENSION
    assert np.allclose(np.linalg.norm(vectors, axis=1), 1, atol=1e-5)
    assert spans[-1]["end_token"] == spans[-1]["total_content_tokens"]
    assert all(span["input_tokens"] <= 512 for span in spans)
    with pytest.raises(ValueError):
        model.encode(text, "query")
