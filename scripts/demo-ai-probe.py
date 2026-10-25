"""Read-only probes, run inside the existing ai-service container via stdin.

Never print configuration, credentials, prompts or model responses.
"""
import json
import math
import os
import sys
import time
import urllib.request


def post(url, body, headers=None, timeout=75):
    req = urllib.request.Request(url, json.dumps(body).encode(),
                                 {'Content-Type': 'application/json', **(headers or {})})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)


start = time.monotonic()
try:
    if sys.argv[1] == 'embedding':
        data = post('http://127.0.0.1:8000/internal/v1/embeddings',
                    {'kind': 'query', 'inputs': ['Контроль готовности демонстрации']},
                    {'X-Service-Token': os.environ['AI_SERVICE_TOKEN']}, 40)
        vector = data['embeddings'][0]
        assert data['dimensions'] == 384 and len(vector) == 384
        assert all(math.isfinite(x) for x in vector)
        assert abs(sum(x*x for x in vector) - 1) < .002
        result = {'ok': True, 'dimensions': 384, 'model': data['model'],
                  'revision': data['revision'], 'format_revision': data['format_revision']}
    elif sys.argv[1] == 'qwen':
        key = os.environ.get('LLM_API_KEY')
        headers = {'Authorization': 'Bearer ' + key} if key else {}
        data = post(os.environ['LLM_BASE_URL'].rstrip('/') + '/chat/completions',
                    {'model': os.environ['LLM_MODEL'], 'messages': [
                        {'role': 'user', 'content': 'Return JSON with one key ready set to true.'}],
                     'temperature': 0, 'max_tokens': 512, 'stream': False,
                     'response_format': {'type': 'json_object'}}, headers)
        choice = data['choices'][0]
        assert choice['finish_reason'] == 'stop'
        assert json.loads(choice['message']['content']) == {'ready': True}
        result = {'ok': True, 'model': os.environ['LLM_MODEL']}
    else:
        raise ValueError('Unknown probe')
    result['ms'] = round((time.monotonic() - start)*1000)
    print(json.dumps(result))
except Exception as error:
    print(json.dumps({'ok': False, 'error_type': type(error).__name__,
                      'ms': round((time.monotonic() - start)*1000)}))
    sys.exit(1)
