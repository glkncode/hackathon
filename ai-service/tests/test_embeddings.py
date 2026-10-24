import asyncio
import json
import os
import time
from pathlib import Path
from types import SimpleNamespace
from uuid import uuid4

import numpy as np
import pytest
from fastapi.testclient import TestClient

from app.application import create_app
from app.embeddings.model import Engine, engine, EmbeddingUnavailable
from app.embeddings.onnx import E5, token_windows
from app.embeddings.schemas import EmbeddingRequest
from app.embeddings.evidence import select_evidence
from app.llm.client import InferenceError
from app.schemas.rag import GenerateRequest


@pytest.fixture(scope='module')
def real():
    path = os.environ.get('EMBEDDING_TEST_MODEL_PATH')
    if not path:
        pytest.skip('Set EMBEDDING_TEST_MODEL_PATH; never download weights in tests')
    return Engine(Path(path))


def test_missing_model_does_not_break_service(tmp_path, monkeypatch):
    monkeypatch.setenv('AI_SERVICE_TOKEN', 'test-token')
    monkeypatch.setattr(engine, 'path', tmp_path)
    monkeypatch.setattr(engine, 'model', None)
    with TestClient(create_app()) as client:
        assert client.get('/health').status_code == 200
        for route, data in [('/internal/v1/embeddings', {'kind':'query','inputs':['test']}),
                            ('/internal/v1/evidence', {})]:
            assert client.post(route, json=data).status_code == 401
            assert client.post(route, json=data, headers={'X-Service-Token':'wrong'}).status_code == 401
        response = client.post('/internal/v1/embeddings', json={'kind':'query','inputs':['test']}, headers={'X-Service-Token':'test-token'})
        assert response.status_code == 503
        assert response.json()['error']['code'] == 'embedding_unavailable'


@pytest.mark.parametrize('kind', ['query', 'passage'])
def test_real_prefix_tokenization_and_batch(real, kind):
    model = real.load()
    texts = ['Проверка русскоязычного фрагмента.', 'hello world', 'Казахстан\t1800 рублей']
    for text in texts:
        separate = model.tokenizer.encode(kind + ':', add_special_tokens=False).ids + model.tokenizer.encode(text, add_special_tokens=False).ids
        assert separate == model.tokenizer.encode(kind + ': ' + text, add_special_tokens=False).ids
    result = real.encode(EmbeddingRequest(kind=kind, inputs=texts))
    assert result['dimensions'] == 384
    assert len(result['embeddings']) == len(texts)
    assert [w['input_index'] for w in result['windows']] == [0, 1, 2]
    for text, vector in zip(texts, result['embeddings'], strict=True):
        expected, _ = model.encode(text, kind)
        assert np.asarray(vector).shape == (384,)
        assert np.isfinite(vector).all()
        assert np.linalg.norm(vector) == pytest.approx(1, abs=1e-6)
        np.testing.assert_allclose(vector, expected[0], atol=2e-6)
    assert real.load() is model


def test_long_context_preserves_tail_and_rejects_long_query(real):
    text = 'Длинный регламент процедуры. ' * 350 + 'Уникальное требование в конце документа.'
    result = real.encode(EmbeddingRequest(kind='passage', inputs=[text]))
    expected, spans = real.load().encode(text, 'passage')
    windows = result['windows']
    assert len(windows) > 1
    assert windows[-1]['end_token'] == windows[-1]['total_content_tokens']
    for i, w in enumerate(windows):
        assert w['start_token'] == spans[i]['start_token']
        assert w['end_token'] == spans[i]['end_token']
        assert w['end_token'] - w['start_token'] <= 508
    np.testing.assert_allclose(result['embeddings'], expected, atol=2e-6)
    with pytest.raises(ValueError):
        real.encode(EmbeddingRequest(kind='query', inputs=[text]))


def test_all_tokens_covered():
    ids = list(range(2200))
    windows = list(token_windows(ids, 508))
    assert {v for _, _, w in windows for v in w} == set(ids)
    assert windows[1][0] == windows[0][1] - 64


@pytest.mark.parametrize('data', [
    {'kind':'query','inputs':[]}, {'kind':'other','inputs':['x']},
    {'kind':'query','inputs':['x']*9}, {'kind':'query','inputs':[' ']},
    {'kind':'query','inputs':['x'],'model':'other'}, {'kind':'query','inputs':[12]},
])
def test_strict_schema(data):
    with pytest.raises(ValueError):
        EmbeddingRequest.model_validate(data)


def test_token_budget_before_inference(real):
    with pytest.raises(ValueError):
        real.encode(EmbeddingRequest(kind='passage', inputs=['слово ' * 10000]*4))


def test_nonfinite_model_output_rejected(real, monkeypatch):
    fake = SimpleNamespace(tokenizer=real.load().tokenizer, bos=0, eos=2, inputs={'input_ids','attention_mask'},
                           session=SimpleNamespace(run=lambda _, values: [np.full((*values['input_ids'].shape, 384), np.nan)]))
    isolated = Engine()
    isolated.model = fake
    with pytest.raises(EmbeddingUnavailable):
        isolated.encode(EmbeddingRequest(kind='query', inputs=['test']))


def test_auth_busy_and_request_limits(monkeypatch):
    monkeypatch.setenv('AI_SERVICE_TOKEN', 'test-token')
    with TestClient(create_app()) as client:
        headers = {'X-Service-Token':'test-token'}
        engine.lock.acquire()
        try:
            assert client.post('/internal/v1/embeddings', json={'kind':'query','inputs':['test']}, headers=headers).status_code == 503
        finally:
            engine.lock.release()
        assert client.post('/internal/v1/embeddings', content='x'*310000, headers=headers).status_code == 413


def test_evidence_rejects_invented_quotes_and_ids():
    sid = str(uuid4())
    req = GenerateRequest(query='Когда проверить датчик?',sources=[dict(chunk_id=sid,document_title='Правила',version_number=1,content='Датчик проверяют каждые шесть месяцев.')])
    class LLM:
        raw = ''
        async def generate(self, messages): return self.raw
    llm = LLM()
    for evidence in [[{'chunk_id':str(uuid4()),'quote':'Датчик проверяют'}],
                     [{'chunk_id':sid,'quote':' '*8}],
                     [{'chunk_id':sid,'quote':'проверяют ежегодно'}],
                     [{'chunk_id':sid,'quote':'Датчик проверяют'}]*2]:
        llm.raw = json.dumps({'evidence':evidence})
        with pytest.raises(InferenceError): asyncio.run(select_evidence(req,llm))
    llm.raw = '{"evidence": []}'
    assert asyncio.run(select_evidence(req,llm)).evidence == []
    llm.raw = json.dumps({'evidence':[{'chunk_id':sid,'quote':'Датчик проверяют каждые шесть месяцев.'}]})
    assert len(asyncio.run(select_evidence(req,llm)).evidence) == 1
    req.sources[0].content='Датчик\u00a0проверяют\nкаждые шесть\tмесяцев.'
    assert len(asyncio.run(select_evidence(req,llm)).evidence)==1
    llm.raw=json.dumps({'evidence':[{'chunk_id':sid,'quote':'Датчик проверяют каждые семь месяцев.'}]})
    with pytest.raises(InferenceError):asyncio.run(select_evidence(req,llm))


def test_timeout_keeps_cpu_slot_until_operation_finishes(monkeypatch):
    monkeypatch.setenv('AI_SERVICE_TOKEN','test-token')
    monkeypatch.setenv('EMBEDDING_TIMEOUT_SECONDS','0.01')
    def slow(body):
        time.sleep(.2)
        raise EmbeddingUnavailable()
    monkeypatch.setattr(engine,'encode',slow)
    with TestClient(create_app()) as client:
        payload={'kind':'query','inputs':['test']};headers={'X-Service-Token':'test-token'}
        assert client.post('/internal/v1/embeddings',json=payload,headers=headers).status_code == 504
        assert engine.lock.locked()
        assert client.post('/internal/v1/embeddings',json=payload,headers=headers).status_code == 503
        time.sleep(.25)
        assert not engine.lock.locked()


def test_missing_model_recovery_and_single_load(real,monkeypatch):
    from app.embeddings import model as module
    calls=[]
    original=module.E5
    def counted(*args,**kwargs):
        calls.append(1)
        return original(*args,**kwargs)
    monkeypatch.setattr(module,'E5',counted)
    instance=Engine(real.path)
    instance.encode(EmbeddingRequest(kind='query',inputs=['первый вопрос']))
    instance.encode(EmbeddingRequest(kind='query',inputs=['второй вопрос']))
    assert len(calls)==1
