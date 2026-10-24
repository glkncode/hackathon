"""Tested experimental E5 core, promoted unchanged into the application.

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
