"""Explicitly downloaded local e5 ONNX model. No application integration/downloads.

Each passage window retains its original PostgreSQL chunk ID. Cosine similarity
is max over windows; a long chunk is never silently truncated to 512 tokens.
"""

from pathlib import Path

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

MODEL = "intfloat/multilingual-e5-small"
REVISION = "614241f622f53c4eeff9890bdc4f31cfecc418b3"
DIMENSION = 384
MAX_TOKENS = 512


def token_windows(ids, capacity, overlap=64):
    if capacity <= overlap or not ids:
        raise ValueError("Invalid window parameters or empty input")
    start = 0
    while True:
        end = min(start + capacity, len(ids))
        yield start, end, ids[start:end]
        if end == len(ids):
            break
        start = end - overlap


class E5:
    def __init__(self, path: Path, threads=2):
        self.tokenizer = Tokenizer.from_file(str(path / "tokenizer.json"))
        self.tokenizer.no_truncation()
        self.tokenizer.no_padding()
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        self.session = ort.InferenceSession(
            str(path / "onnx/model.onnx"), sess_options=options,
            providers=["CPUExecutionProvider"],
        )
        self.inputs = {i.name for i in self.session.get_inputs()}
        self.bos = self.tokenizer.token_to_id("<s>")
        self.eos = self.tokenizer.token_to_id("</s>")
        if self.bos is None or self.eos is None:
            raise ValueError("Expected multilingual e5/XLM-R tokenizer")

    def encode(self, text, kind):
        if kind not in {"query", "passage"} or not text.strip():
            raise ValueError("Expected nonempty query or passage")
        # XLM-R encodes a trailing standalone space as a separate token. Tokenize
        # the colon without that space; text's leading word marker supplies it.
        # Tests compare these IDs with tokenizing the complete prefixed string.
        prefix = self.tokenizer.encode(kind + ":", add_special_tokens=False).ids
        ids = self.tokenizer.encode(text, add_special_tokens=False).ids
        capacity = MAX_TOKENS - len(prefix) - 2
        if kind == "query" and len(ids) > capacity:
            raise ValueError("Question exceeds model context; refusing truncation")
        vectors, spans = [], []
        for start, end, window in token_windows(ids, capacity):
            tokens = np.asarray([[self.bos, *prefix, *window, self.eos]], dtype=np.int64)
            mask = np.ones_like(tokens)
            inputs = {"input_ids": tokens, "attention_mask": mask}
            if "token_type_ids" in self.inputs:
                inputs["token_type_ids"] = np.zeros_like(tokens)
            hidden = self.session.run(None, inputs)[0]
            if hidden.ndim != 3 or hidden.shape[:2] != tokens.shape:
                raise ValueError("Expected token embeddings with attention-mask pooling")
            vector = (hidden * mask[..., None]).sum(1) / mask.sum(1)[:, None]
            vector = vector[0].astype(np.float32)
            norm = np.linalg.norm(vector)
            if vector.shape != (DIMENSION,) or not np.isfinite(vector).all() or norm <= 0:
                raise ValueError("Invalid embedding")
            vectors.append(vector / norm)
            spans.append({"start_token": start, "end_token": end,
                          "input_tokens": int(tokens.size), "total_content_tokens": len(ids)})
        return np.asarray(vectors), spans


def cosine_candidates(query, vectors, window_ids):
    if query.shape != (1, DIMENSION) or vectors.ndim != 2 or vectors.shape[1] != DIMENSION:
        raise ValueError("Incompatible index/query dimensions")
    if len(window_ids) != len(vectors):
        raise ValueError("Window provenance mismatch")
    scores = vectors @ query[0]
    by_chunk = {}
    for source_id, score in zip(window_ids, scores, strict=True):
        by_chunk[source_id] = max(float(score), by_chunk.get(source_id, -1.0))
    return sorted(by_chunk.items(), key=lambda item: (-item[1], item[0]))


def rrf(lists, limit=10, k=60):
    scores = {}
    for candidates in lists:
        seen = set()
        for rank, (source_id, _) in enumerate(candidates, 1):
            if source_id in seen:
                raise ValueError("Duplicate source ID in branch")
            seen.add(source_id)
            scores[source_id] = scores.get(source_id, 0) + 1 / (k + rank)
    return sorted(scores.items(), key=lambda item: (-item[1], item[0]))[:limit]


def metrics(rows):
    positive = [r for r in rows if r["gold_chunk_ids"]]
    negative = [r for r in rows if not r["gold_chunk_ids"]]
    recall5, recall10, reciprocal, relevant, returned = [], [], [], 0, 0
    for r in positive:
        gold, ids = set(r["gold_chunk_ids"]), [x[0] for x in r["results"]]
        recall5.append(len(gold.intersection(ids[:5])) / len(gold))
        recall10.append(len(gold.intersection(ids[:10])) / len(gold))
        reciprocal.append(next((1 / rank for rank, sid in enumerate(ids, 1) if sid in gold), 0))
    for r in rows:
        ids = [x[0] for x in r["results"]]
        relevant += len(set(ids).intersection(r["gold_chunk_ids"]))
        returned += len(ids)
    average = lambda values: float(np.mean(values)) if values else None
    times = [r["latency_ms"] for r in rows]
    return {
        "positive_queries": len(positive), "negative_queries": len(negative),
        "recall_at_5": average(recall5), "recall_at_10": average(recall10),
        "mrr": average(reciprocal), "candidate_precision": relevant / returned if returned else None,
        "negative_candidate_rate": average([bool(r["results"]) for r in negative]),
        "latency_ms_p50": float(np.percentile(times, 50)),
        "latency_ms_p95": float(np.percentile(times, 95)),
    }
