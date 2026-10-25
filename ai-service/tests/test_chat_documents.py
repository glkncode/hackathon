import copy
import json

import pytest

from test_rag import client, inference, TOKEN  # shared real HTTP LLM stub/lifespan

BODY = dict(query="Подготовь документ", stage="", templates=[dict(id="registered", name="Реальный модуль", description="Назначение")],
            fields=[dict(key="person", type="text")], values={}, expected_field="person")
PLAN = dict(intent="create_document", template_ids=["registered"], changes=[], target_field="", needs_clarification=False)


def post(client, body=BODY, token=TOKEN):
    return client.post("/internal/v1/chat-documents/interpret", json=body, headers={"X-Service-Token": token})


def test_interpret_uses_existing_llm_client(client, inference):
    inference[1].output = PLAN
    assert post(client).json() == dict(PLAN, changes={})
    path, payload = inference[1].calls[0]
    assert path == "/v1/chat/completions"
    assert payload["model"] == "mock-model"
    assert payload["response_format"]["type"] == "json_schema"
    changes = payload["response_format"]["json_schema"]["schema"]["properties"]["changes"]
    assert changes["type"] == "array"
    assert changes["items"]["anyOf"][0]["properties"]["field"]["const"] == "person"
    assert changes["items"]["anyOf"][0]["additionalProperties"] is False
    schema = payload["response_format"]["json_schema"]["schema"]
    assert schema["properties"]["template_ids"]["items"]["enum"] == ["registered"]
    assert schema["properties"]["target_field"]["enum"] == ["", "person"]
    assert json.loads(payload["messages"][1]["content"]) == BODY


@pytest.mark.parametrize("mutation", [
    dict(intent="execute_shell"), dict(template_ids=["invented"]),
    dict(changes={"unknown": "x"}), dict(changes={"person": "x"}),
    dict(target_field="unknown"), dict(needs_clarification="false"),
    dict(unexpected="x"), dict(template_ids=None), dict(changes=None),
    dict(intent="answer_field", changes={"person": "x"}, needs_clarification=True),
])
def test_invalid_interpretation_is_not_executable(client, inference, mutation):
    inference[1].output = dict(PLAN, **mutation)
    response = post(client)
    assert response.status_code == 502
    assert "unknown" not in response.text


@pytest.mark.parametrize("raw", ["{}", '{"intent":"create_document","intent":"generate_document"}', '```json\n{}\n```'])
def test_strict_json(client, inference, raw):
    inference[1].output = raw
    assert post(client).status_code == 502


def test_private_authentication_before_model(client, inference):
    assert post(client, token="wrong").status_code == 401
    assert len(inference[1].calls) == 0


def test_field_answer_and_question_are_separate_contracts(client, inference):
    inference[1].output = dict(PLAN, intent="answer_field", template_ids=[], changes=[dict(field="person", value="Петров")])
    assert post(client).json()["changes"] == {"person": "Петров"}
    inference[1].output = dict(PLAN, intent="knowledge_question", template_ids=[])
    assert post(client).json()["intent"] == "knowledge_question"


def test_upstream_error_safe(client, inference):
    inference[1].status = 503
    assert post(client).status_code == 503


def test_bounded_request(client, inference):
    body = copy.deepcopy(BODY)
    body["values"] = {"person": "x" * (301 * 1024)}
    assert post(client, body).status_code == 413
    assert not inference[1].calls


def test_repeater_response_grammar_uses_registered_required_cells(client, inference):
    body = copy.deepcopy(BODY)
    body["fields"] = [dict(key="attendees", label="Участники", type="repeater", item_fields=[
        dict(key="role", label="Роль", required=True),
        dict(key="note", label="Примечание", required=False),
    ])]
    body["expected_field"] = "attendees"
    inference[1].output = PLAN
    assert post(client, body).status_code == 200
    schema = inference[1].calls[0][1]["response_format"]["json_schema"]["schema"]
    cells = schema["properties"]["changes"]["items"]["anyOf"][0]["properties"]["value"]["anyOf"][0]["items"]
    assert cells["required"] == ["role"]
    assert set(cells["properties"]) == {"role", "note"}
    assert cells["additionalProperties"] is False


def test_list_operations_preserve_other_cells_and_order(client, inference):
    body = copy.deepcopy(BODY)
    body["fields"] = [dict(key="rows", type="repeater", item_fields=[dict(key="name"), dict(key="role")])]
    body["expected_field"] = "rows"
    body["values"] = {"rows": [dict(name="Первый", role="A"), dict(name="Второй", role="B")]}
    operations = [dict(op="append", values=dict(name="Третий", role="C")),
                  dict(op="update", index=2, values=dict(name="Изменённый")), dict(op="delete", index=3)]
    inference[1].output = dict(PLAN, intent="edit_document_field", template_ids=[],
                               changes=[dict(field="rows", value=dict(operations=operations))])
    assert post(client, body).json()["changes"]["rows"] == [dict(name="Первый", role="A"), dict(name="Изменённый", role="B")]
    assert body["values"]["rows"][1]["name"] == "Второй"


@pytest.mark.parametrize("operation", [
    dict(op="update", index=0, values=dict(name="x")),
    dict(op="delete", index=2), dict(op="delete", index=True),
    dict(op="update", index=1, values=dict(invented="x")),
    dict(op="append", values={}), dict(op="execute", values={}),
    dict(op="replace", values="x"), dict(op="delete", index=1, extra=True),
])
def test_invalid_list_operation_is_not_executable(client, inference, operation):
    body = copy.deepcopy(BODY)
    body.update(fields=[dict(key="rows", type="repeater", item_fields=[dict(key="name")])],
                values={"rows": [dict(name="original")]}, expected_field="rows")
    inference[1].output = dict(PLAN, intent="edit_document_field", template_ids=[],
                               changes=[dict(field="rows", value=dict(operations=[operation]))])
    assert post(client, body).status_code == 502


def test_changes_can_follow_user_order_and_cannot_repeat_field(client, inference):
    body = copy.deepcopy(BODY)
    body["fields"].append(dict(key="later", type="text"))
    inference[1].output = dict(PLAN, intent="answer_field", template_ids=[], changes=[
        dict(field="later", value="second"), dict(field="person", value="first")])
    assert post(client, body).json()["changes"] == dict(later="second", person="first")
    inference[1].output["changes"].append(dict(field="person", value="duplicate"))
    assert post(client, body).status_code == 502
