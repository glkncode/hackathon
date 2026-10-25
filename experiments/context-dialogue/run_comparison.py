"""Frozen Resolver off/on comparison; no main container/schema is modified."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import shutil
import sys


ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--corpus', required=True)
    parser.add_argument('--run', required=True)
    parser.add_argument('--baseline', required=True)
    parser.add_argument('--modes', default='baseline,context')
    parser.add_argument('--same-go-baseline', action='store_true', help='Compare Resolver off/on on the same Go implementation')
    args = parser.parse_args()
    baseline = Path(args.baseline).resolve()
    corpus = Path(args.corpus).resolve()
    for mode in args.modes.split(','):
        assert mode in {'baseline', 'context'}
        env = dict(os.environ, CONTEXT_EVAL_RUN=f'{args.run}-{mode}', PYTHONIOENCODING='utf-8')
        cmd = [sys.executable, str(Path(__file__).with_name('run.py')), '--evaluate',
               '--corpus', str(corpus), '--mode', mode]
        if mode == 'baseline':
            cmd += ['--ai-source', str(baseline/'ai-service')]
            if not args.same_go_baseline:
                cmd += ['--go-overlay', str(baseline/'go-overlay.json')]
        source = baseline/'ai-service/app/rag' if mode == 'baseline' else ROOT/'ai-service/app/rag'
        before = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in source.glob('*.py')}
        go_files = [p for p in (ROOT/'internal').rglob('*.go') if not p.name.endswith('_test.go')]
        go_before = {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in go_files}
        subprocess.run(cmd, cwd=ROOT, env=env, check=True)
        out = ROOT/'.local-integration/context-dialogue'/env['CONTEXT_EVAL_RUN']
        files = list((ROOT/'ai-service/app/rag').glob('*.py'))
        if mode == 'baseline':
            files = list((baseline/'ai-service/app/rag').glob('*.py'))
        after = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
        assert before == after, 'Variant source changed during inference'
        assert go_before == {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in go_files}, 'Go source changed during inference'
        shutil.copytree(source, out/'rag-source', ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
        (out/'variant.json').write_text(json.dumps({
            'mode': mode, 'context_resolver': mode != 'baseline',
            'corpus_sha256': hashlib.sha256(corpus.read_bytes()).hexdigest(),
            'python_source_hashes': after,
            'baseline_snapshot': str(baseline),
            'same_go_baseline': args.same_go_baseline,
            'go_source_hashes': go_before,
        }, indent=2), encoding='utf-8')


if __name__ == '__main__':
    main()
