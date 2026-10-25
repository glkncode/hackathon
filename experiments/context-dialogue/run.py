"""Isolated local tests; never modifies the main containers, env or database.

Uses the already configured Qwen and read-only model volume. The opt-in real
evaluation calls Go ChatService/repositories, pgvector, E5, Evidence Gate and Qwen.
"""
import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / '.local-integration/context-dialogue'
NAMES = ['context-rag-eval-ai', 'context-rag-eval-pg']


def docker(*args, **kw):
    return subprocess.check_output(['docker', *args], text=True, encoding='utf-8', **kw)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--evaluate', action='store_true')
    parser.add_argument('--python', action='store_true')
    parser.add_argument('--holdout', action='store_true')
    parser.add_argument('--corpus')
    parser.add_argument('--ai-source')
    parser.add_argument('--go-overlay')
    parser.add_argument('--go-package-parallelism', type=int,
                        help='Optional Go package concurrency for shared disposable PostgreSQL')
    parser.add_argument('--mode', choices=['baseline', 'context'])
    args_cli = parser.parse_args()
    if args_cli.go_package_parallelism is not None and args_cli.go_package_parallelism < 1:
        parser.error('--go-package-parallelism must be positive')
    OUT.mkdir(exist_ok=True)
    global_eval_out = os.environ.get('CONTEXT_EVAL_RUN', 'initial')
    evaluation_out = OUT / global_eval_out
    evaluation_out.mkdir(exist_ok=False)  # never replace previous observations/settings
    existing = set(docker('ps', '-a', '--format', '{{.Names}}').splitlines())
    if existing.intersection(NAMES):
        raise RuntimeError('Owned test containers already exist; inspect before rerunning')
    info = json.loads(docker('inspect', 'hackathon-ai-service-1'))[0]
    inherited = dict(item.split('=', 1) for item in info['Config']['Env'])
    env = dict(os.environ)
    for key in ['LLM_BASE_URL', 'LLM_MODEL', 'LLM_API_KEY', 'LLM_ALLOW_REMOTE', 'LLM_TIMEOUT']:
        env[key] = inherited.get(key, '')
    env['AI_SERVICE_TOKEN'] = 'isolated-context-evaluation'
    env['EMBEDDING_MODEL_PATH'] = '/models/e5-small'
    env['PYTHONDONTWRITEBYTECODE'] = '1'
    (evaluation_out/'inference-settings.json').write_text(json.dumps({
        'model': env['LLM_MODEL'], 'temperature': 0, 'max_tokens': 4096,
        'resolver': args_cli.mode == 'context',
    }, indent=2), encoding='utf-8')
    model = next(m for m in info['Mounts'] if m['Destination'] == '/models')
    started = []
    try:
        docker('run', '-d', '--name', NAMES[1], '-p', '127.0.0.1:15439:5432',
               '--tmpfs', '/var/lib/postgresql/data', '-e', 'POSTGRES_PASSWORD=context-test-only',
               '-e', 'POSTGRES_DB=context_test', 'pgvector/pgvector:pg17-bookworm')
        started.append(NAMES[1])
        args = ['run', '-d', '--name', NAMES[0], '-p', '127.0.0.1:18039:8000',
                '--mount', f'type=bind,source={Path(args_cli.ai_source).resolve() if args_cli.ai_source else ROOT / "ai-service"},target=/app,readonly',
                '--mount', f'type=bind,source={ROOT / "scripts"},target=/scripts,readonly',
                '--mount', f'type=volume,source={model["Name"]},target=/models,readonly']
        for key in ['LLM_BASE_URL', 'LLM_MODEL', 'LLM_API_KEY', 'LLM_ALLOW_REMOTE', 'LLM_TIMEOUT',
                    'AI_SERVICE_TOKEN', 'EMBEDDING_MODEL_PATH', 'PYTHONDONTWRITEBYTECODE']:
            args.extend(['-e', key])
        docker(*args, info['Image'], env=env)
        started.append(NAMES[0])
        import httpx
        for _ in range(60):
            state = json.loads(docker('inspect', '--format', '{{json .State}}', NAMES[0]))
            if state['Status'] == 'exited':
                raise RuntimeError('Isolated AI service exited before evaluation')
            try:
                r = httpx.get('http://127.0.0.1:18039/health', trust_env=False, timeout=2)
                if r.status_code == 200 and 'accepting connections' in docker('exec', NAMES[1], 'pg_isready', '-U', 'postgres'):
                    break
            except Exception:
                pass
            time.sleep(.5)
        else:
            raise RuntimeError('Isolated services did not start')
        if args_cli.python:
            subprocess.run(['docker', 'exec', NAMES[0], 'python', '-m', 'pytest', '-q',
                            '-p', 'no:cacheprovider', 'tests/test_resolver.py',
                            'tests/test_rag.py', 'tests/test_embeddings.py',
                            'tests/test_rag_templates.py', 'tests/test_application.py'], check=True)
        env.update(TEST_DATABASE_URL='postgres://postgres:context-test-only@127.0.0.1:15439/context_test?sslmode=disable',
                   CONTEXT_EVAL_AI_URL='http://127.0.0.1:18039',
                   CONTEXT_EVAL_TOKEN=env['AI_SERVICE_TOKEN'],
                   CONTEXT_EVAL_OUT=str(evaluation_out))
        if args_cli.holdout:
            env['CONTEXT_EVAL_CORPUS'] = str(Path(__file__).with_name('corpus-holdout.json'))
        if args_cli.corpus:
            env['CONTEXT_EVAL_CORPUS'] = str(Path(args_cli.corpus).resolve())
        if args_cli.mode:
            env['CONTEXT_EVAL_MODE'] = args_cli.mode
        go_args = ['go', 'test']
        if args_cli.go_package_parallelism is not None:
            go_args += ['-p', str(args_cli.go_package_parallelism)]
        if args_cli.go_overlay:
            go_args.append('-overlay=' + str(Path(args_cli.go_overlay).resolve()))
        if args_cli.evaluate:
            subprocess.run([*go_args, './internal/repository/postgres', '-run',
                            '^TestIntegrationContextEvaluation$', '-count=1', '-v', '-timeout=30m'],
                           cwd=ROOT, env=env, check=True)
            metrics = httpx.get('http://127.0.0.1:18039/internal/v1/rag/resolver-metrics',
                                headers={'X-Service-Token': env['AI_SERVICE_TOKEN']}, trust_env=False)
            metrics.raise_for_status()
            (evaluation_out/'runtime-metrics.json').write_text(
                json.dumps(metrics.json(), indent=2), encoding='utf-8')
        else:
            env.pop('CONTEXT_EVAL_OUT', None)  # full suite must not silently run model evaluation
            subprocess.run([*go_args, './...', '-count=1', '-timeout=10m'],
                           cwd=ROOT, env=env, check=True)
    finally:
        try:
            if NAMES[0] in started:
                # Counts/labels only from application logs; raw answers live only in
                # the explicitly opted-in, ignored observations artifact.
                (evaluation_out/'ai-runtime.log').write_text(
                    docker('logs', NAMES[0], stderr=subprocess.STDOUT), encoding='utf-8')
        finally:
            for name in reversed(started):
                docker('rm', '-f', name)


if __name__ == '__main__':
    main()
