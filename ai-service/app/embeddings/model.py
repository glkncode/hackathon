"""One lazy CPU session; no network I/O. Bounded requests, windows and concurrency."""
import hashlib
import os
import threading
from pathlib import Path

import numpy as np

from app.embeddings.onnx import E5, MODEL, REVISION, DIMENSION, MAX_TOKENS, token_windows

FORMAT = 'e5-exact-prefix-v1-title-content-512-overlap64-mean-l2-max'
HASHES = {
    'onnx/model.onnx': 'ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665',
    'tokenizer.json': '0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39',
}


class EmbeddingUnavailable(Exception):
    pass


class Engine:
    def __init__(self, path=None):
        self.path = Path(path or os.environ.get('EMBEDDING_MODEL_PATH', '/models/e5-small'))
        self.lock = threading.Lock()
        self.model = None

    def load(self):
        if self.model is None:
            try:
                for name, expected in HASHES.items():
                    with (self.path / name).open('rb') as f:
                        if hashlib.file_digest(f, 'sha256').hexdigest() != expected:
                            raise EmbeddingUnavailable()
                self.model = E5(self.path)
            except Exception:
                raise EmbeddingUnavailable() from None
        return self.model

    def encode(self, request):
        model = self.load()
        # Prefix/token-window construction is exactly the tested experimental code.
        # Prepare the bounded batch before inference; reject over-budget input
        # rather than truncating or leaving an unbounded queue in memory.
        prefix = model.tokenizer.encode(request.kind + ':', add_special_tokens=False).ids
        capacity = MAX_TOKENS - len(prefix) - 2
        sequences, mapping = [], []
        total = 0
        for i, text in enumerate(request.inputs):
            ids = model.tokenizer.encode(text, add_special_tokens=False).ids
            if request.kind == 'query' and len(ids) > capacity:
                raise ValueError('Question exceeds model context')
            for j, (start, end, window) in enumerate(token_windows(ids, capacity)):
                seq = [model.bos, *prefix, *window, model.eos]
                total += len(seq)
                if total > 32768 or len(sequences) >= 128:
                    raise ValueError('Embedding token/window budget exceeded')
                sequences.append(seq)
                mapping.append(dict(input_index=i, window_index=j, start_token=start,
                                    end_token=end, total_content_tokens=len(ids)))
        vectors = []
        # True padded ONNX batches, at most 8 x 512 tokens in activation memory.
        for offset in range(0, len(sequences), 8):
            batch = sequences[offset:offset + 8]
            width = max(map(len, batch))
            tokens = np.full((len(batch), width), model.tokenizer.token_to_id('<pad>'), dtype=np.int64)
            mask = np.zeros_like(tokens)
            for row, seq in enumerate(batch):
                tokens[row, :len(seq)] = seq
                mask[row, :len(seq)] = 1
            inputs = {'input_ids': tokens, 'attention_mask': mask}
            if 'token_type_ids' in model.inputs:
                inputs['token_type_ids'] = np.zeros_like(tokens)
            hidden = model.session.run(None, inputs)[0]
            if hidden.shape != (*tokens.shape, DIMENSION):
                raise EmbeddingUnavailable()
            pooled = (hidden * mask[..., None]).sum(1) / mask.sum(1)[:, None]
            norms = np.linalg.norm(pooled, axis=1, keepdims=True)
            if not np.isfinite(pooled).all() or not np.isfinite(norms).all() or (norms <= 0).any():
                raise EmbeddingUnavailable()
            vectors.extend((pooled / norms).astype(np.float32).tolist())
        return dict(model=MODEL, revision=REVISION, dimensions=DIMENSION,
                    format_revision=FORMAT, embeddings=vectors, windows=mapping)


engine = Engine()
