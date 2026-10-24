"""Apply an explicit source-by-source review to immutable observed outputs.

Review labels are evaluation data only. This module is never imported by retrieval,
generation or the main application; it does not change questions or gold sources.
"""

import json

import numpy as np

from benchmark import digest, load, save


def review():
    rows, manifest = load("answers.json"), load("manifest.json")
    annotations = load("review.json")
    if annotations["observed_answers_sha256"] != digest(rows):
        raise RuntimeError("Answers changed after review; new substantive review required")
    assert len(rows) == len(manifest["questions"]) * 3
    assert not load("answer-errors.json")
    summary = {}
    for method in ("fts", "semantic", "hybrid"):
        labels = annotations["methods"][method]
        correct = set(labels["correct_positive_ids"])
        unsupported = set(labels["unsupported_positive_ids"])
        fabricated = set(labels["fabricated_negative_ids"])
        summary[method] = {}
        for split in ("development", "holdout"):
            group = [r for r in rows if r["method"] == method and r["split"] == split]
            positive = [r for r in group if r["gold_chunk_ids"]]
            negative = [r for r in group if not r["gold_chunk_ids"]]
            answered_positive = [r for r in positive if r["answered"]]
            cited = [r for r in group if r["used_source_ids"]]
            valid_citations = all(set(r["used_source_ids"]) <= set(r["retrieved_chunk_ids"]) for r in cited)
            assert valid_citations
            summary[method][split] = {
                "positive_queries": len(positive), "negative_queries": len(negative),
                "grounded_answer_accuracy": sum(r["query_id"] in correct for r in positive) / len(positive),
                "positive_answer_citation_set_faithfulness": sum(r["query_id"] not in unsupported for r in answered_positive) / len(answered_positive),
                "citation_id_membership_valid": valid_citations,
                "false_factual_answer_rate_on_negatives": sum(r["query_id"] in fabricated for r in negative) / len(negative),
                "negative_answered_status_rate": sum(r["answered"] for r in negative) / len(negative),
                "generation_ms_p50": float(np.percentile([r["elapsed_ms"] for r in group], 50)),
                "generation_ms_p95": float(np.percentile([r["elapsed_ms"] for r in group], 95)),
            }
    save("review-summary.json", summary)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    review()
