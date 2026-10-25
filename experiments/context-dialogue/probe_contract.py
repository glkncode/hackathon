"""Development-only replay of failed resolver requests through the same Qwen."""
import argparse
import asyncio
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT/'ai-service'))
from app.core.config import LLMConfig
from app.llm.client import LLMClient
from app.rag.resolver import SYSTEM_PROMPT, ResolveRequest, ResolveResponse
from app.rag.generator import unique_object


async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('observations')
    parser.add_argument('output')
    args = parser.parse_args()
    out = Path(args.output)
    assert not out.exists(), 'Do not overwrite inference artifacts'
    info = json.loads(subprocess.check_output(['docker','inspect','hackathon-ai-service-1'],encoding='utf-8'))[0]
    inherited = dict(e.split('=',1) for e in info['Config']['Env'])
    for key in ('LLM_BASE_URL','LLM_MODEL','LLM_API_KEY','LLM_ALLOW_REMOTE','LLM_TIMEOUT'):
        os.environ[key] = inherited.get(key,'')
    rows = json.loads(Path(args.observations).read_text(encoding='utf-8'))
    client = LLMClient(LLMConfig.from_env())
    results = []
    try:
        for row in rows:
            trace = row['Trace'].get('resolver',{})
            if trace.get('error_code') != 'resolver_invalid_response':
                continue
            req = ResolveRequest.model_validate(trace['request'])
            raw = await client.generate([
                {'role':'system','content':SYSTEM_PROMPT},
                {'role':'user','content':json.dumps({'query':req.query,'history':[
                    {'message_index':i,**m.model_dump()} for i,m in enumerate(req.history)]},ensure_ascii=False)},
            ])
            error = None
            try:
                ResolveResponse.model_validate(json.loads(raw,object_pairs_hook=unique_object)).validate_for(req)
            except (ValueError,TypeError) as exc:
                error = str(exc)
            results.append({'scenario':row['Scenario'],'step':row['Step'],'raw':raw,'validation_error':error})
            if len(results) == 3: break
    finally:
        await client.close()
    out.write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf-8')
    print(f'Saved {len(results)} development replays')


if __name__ == '__main__':
    asyncio.run(main())
