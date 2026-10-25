"""Protocol boundaries only; actual completeness is graded on real Qwen runs."""
import asyncio
import json
from unittest.mock import AsyncMock
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient

from app.application import create_app
from app.embeddings.evidence import select_evidence
from app.llm.client import InferenceError
from app.rag.generator import generate
from app.schemas.rag import GenerateRequest


def request():
    return GenerateRequest.model_validate({
        'query': 'Как провести операцию и сколько она стоит?',
        'sources': [
            {'chunk_id': str(uuid4()), 'document_title': 'Порядок', 'version_number': 1,
             'content': 'Оператор регистрирует обращение.'},
            {'chunk_id': str(uuid4()), 'document_title': 'Порядок', 'version_number': 1,
             'content': 'После регистрации проверяющий разрешает операцию.'},
            {'chunk_id': str(uuid4()), 'document_title': 'Иной порядок', 'version_number': 1,
             'content': 'Помещение убирают ежедневно.'},
        ],
    })


@pytest.mark.parametrize('corruption', [None, 'unknown_id', 'invented_quote', 'duplicate'])
def test_multiple_independent_evidence_keeps_strict_validation(corruption, caplog):
    req = request()
    evidence = [{'chunk_id': str(s.chunk_id), 'quote': s.content} for s in req.sources[:2]]
    if corruption == 'unknown_id':
        evidence[1]['chunk_id'] = str(uuid4())
    elif corruption == 'invented_quote':
        evidence[1]['quote'] = 'Проверка не требуется для операции.'
    elif corruption == 'duplicate':
        evidence.append(evidence[0])
    llm = AsyncMock(generate=AsyncMock(return_value=json.dumps({'evidence': evidence})))
    if corruption:
        with pytest.raises(InferenceError) as error:
            asyncio.run(select_evidence(req, llm))
        assert error.value.code == 'evidence_invalid_response'
        reason = {'unknown_id': 'unknown_source_id', 'invented_quote': 'quote_mismatch',
                  'duplicate': 'duplicate_source_id'}[corruption]
        assert f'reason={reason}' in caplog.text
        assert all(str(s.chunk_id) not in caplog.text and s.content not in caplog.text
                   for s in req.sources)
        assert req.query not in caplog.text
    else:
        result = asyncio.run(select_evidence(req, llm))
        assert [e.chunk_id for e in result.evidence] == [s.chunk_id for s in req.sources[:2]]
    assert llm.generate.await_count == 1  # no retry or automatic neighbour inclusion


@pytest.mark.parametrize('raw,reason', [
    ('{"evidence":[],"evidence":[]}', 'invalid_json'),
    ('not-json', 'invalid_json'),
    ('{"evidence":[{"unexpected":"private-model-output"}]}', 'invalid_schema'),
])
def test_validation_diagnostics_never_log_model_body(raw, reason, caplog):
    llm = AsyncMock(generate=AsyncMock(return_value=raw))
    with pytest.raises(InferenceError) as error:
        asyncio.run(select_evidence(request(), llm))
    assert error.value.code == 'evidence_invalid_response'
    assert f'reason={reason}' in caplog.text
    assert raw not in caplog.text and 'private-model-output' not in caplog.text
    assert llm.generate.await_count == 1


def test_partial_answer_preserves_two_citations_without_extra_call():
    req = request()
    body = {'status': 'answered',
            'answer': 'Оператор регистрирует обращение, затем проверяющий разрешает операцию. '
                      'Стоимость в предоставленных сведениях не подтверждена.',
            'used_source_ids': [str(s.chunk_id) for s in req.sources[:2]],
            'clarification_question': None}
    llm = AsyncMock(generate=AsyncMock(return_value=json.dumps(body)))
    result = asyncio.run(generate(req, llm))
    assert result.model_dump(mode='json') == body
    assert llm.generate.await_count == 1


def test_evidence_model_request_preserves_untrusted_data():
    req = request()
    llm = AsyncMock(generate=AsyncMock(return_value='{"evidence":[]}'))
    asyncio.run(select_evidence(req, llm))
    messages = llm.generate.call_args.args[0]
    assert json.loads(messages[1]['content']) == req.model_dump(mode='json')
    assert req.query not in messages[0]['content']
    assert llm.generate.await_count == 1


@pytest.mark.parametrize('failure', [InferenceError('llm_unavailable', 503),
                                     asyncio.CancelledError()])
def test_evidence_failure_propagates_without_retry(failure):
    llm = AsyncMock(generate=AsyncMock(side_effect=failure))
    with pytest.raises(type(failure)):
        asyncio.run(select_evidence(request(), llm))
    assert llm.generate.await_count == 1


def test_evidence_endpoint_auth_and_wire_contract(monkeypatch):
    monkeypatch.setenv('AI_SERVICE_TOKEN', 'evidence-test-token')
    req = request()
    payload = {'evidence': [{'chunk_id': str(s.chunk_id), 'quote': s.content}
                            for s in req.sources[:2]]}
    with TestClient(create_app()) as client:
        client.app.state.llm = AsyncMock(generate=AsyncMock(return_value=json.dumps(payload)))
        assert client.post('/internal/v1/evidence', json={}).status_code == 401
        response = client.post('/internal/v1/evidence', json=req.model_dump(mode='json'),
                               headers={'X-Service-Token': 'evidence-test-token'})
        assert response.status_code == 200
        assert response.json() == payload
        assert client.app.state.llm.generate.await_count == 1
