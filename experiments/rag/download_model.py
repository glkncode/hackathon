"""Explicit operator action, never called from builds or application startup."""

import argparse
from pathlib import Path

from huggingface_hub import hf_hub_download

from embedding import MODEL, REVISION

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
for filename in ("onnx/model.onnx", "tokenizer.json", "tokenizer_config.json"):
    print(hf_hub_download(MODEL, filename, revision=REVISION, local_dir=args.output))
