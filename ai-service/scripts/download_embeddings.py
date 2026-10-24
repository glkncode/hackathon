"""Explicit operator provisioning. Never imported by application/build/startup."""
import argparse
import hashlib
from pathlib import Path

from huggingface_hub import hf_hub_download

from app.embeddings.model import HASHES
from app.embeddings.onnx import MODEL, REVISION

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, default=Path('/models/e5-small'))
args = parser.parse_args()
for name, expected in HASHES.items():
    path = hf_hub_download(MODEL, name, revision=REVISION, local_dir=args.output)
    with open(path, 'rb') as f:
        if hashlib.file_digest(f, 'sha256').hexdigest() != expected:
            raise RuntimeError('Downloaded embedding artifact checksum mismatch')
print('Pinned embedding artifacts verified')
