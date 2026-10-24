"""Version 2: bounded declarative configuration. No executable expressions."""
import json
from typing import Literal
from pydantic import BaseModel, ConfigDict, Field
from app.schemas.templates import FieldDefinition


class Strict(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class Condition(Strict):
    op: Literal['equals', 'not_equals', 'present', 'all', 'any', 'not',
                'date_gte', 'date_lte', 'field_equals', 'field_not_equals', 'count_gt']
    field: str | None = None
    other_field: str | None = None
    value: str | int | bool | None = None
    args: list['Condition'] = Field(default_factory=list, max_length=8)


class ItemField(Strict):
    key: str = Field(pattern=r'^[a-z][a-z0-9_]{0,63}$')
    label: str = Field(min_length=1, max_length=255)
    required: bool = True
    max_length: int = Field(default=1000, ge=1, le=8000)


class ProfileField(FieldDefinition):
    model_config = ConfigDict(extra='forbid', strict=True)
    type: Literal['text', 'multiline_text', 'person', 'date', 'person_list', 'choice', 'repeater', 'text_list']
    required_if: Condition | None = None
    visible_if: Condition | None = None
    options: list[str] = Field(default_factory=list, max_length=16)
    item_fields: list[ItemField] = Field(default_factory=list, max_length=8)
    minimum_items: int = Field(default=1, ge=0, le=30)
    maximum_items: int = Field(default=30, ge=1, le=30)
    source: 'Source | None' = None


class Source(Strict):
    name: str = Field(min_length=1, max_length=255)
    version: str = Field(min_length=1, max_length=255)
    section: str = Field(min_length=1, max_length=255)


class Segment(Strict):
    literal: str | None = Field(default=None, max_length=8000)
    field: str | None = None
    join: str = Field(default=', ', max_length=16)
    when: Condition | None = None
    then: list['Segment'] = Field(default_factory=list, max_length=32)
    otherwise: list['Segment'] = Field(default_factory=list, max_length=32)


class Anchor(Strict):
    paragraph: int = Field(ge=0, le=100000)
    text: str = Field(max_length=8000)


class Edit(Strict):
    paragraph: int = Field(ge=0, le=100000)
    match: str = Field(min_length=1, max_length=8000)
    value: list[Segment] = Field(max_length=32)


class Block(Strict):
    start: Anchor
    end: Anchor
    include_if: Condition


class Repeater(Strict):
    field: str
    table: int = Field(ge=0, le=100)
    start_row: int = Field(ge=0, le=100)
    end_row: int = Field(ge=0, le=100)
    anchor: str = Field(min_length=1, max_length=8000)
    cells: list[list[Segment]] = Field(min_length=1, max_length=8)
    keep_rows_together: bool = False
    repeat_header: bool = False


class Cleanup(Strict):
    approval_table: int = Field(ge=0, le=100)
    approval_text: str = Field(min_length=1, max_length=8000)
    marker: Anchor
    notes_start: Anchor
    notes_end: Anchor
    protected: list[Anchor] = Field(min_length=2, max_length=8)
    remove_paragraphs: list[Anchor] = Field(default_factory=list, max_length=8)


class Rule(Strict):
    id: str = Field(pattern=r'^[a-z][a-z0-9_.-]{0,100}$')
    category: Literal['blocking', 'warning', 'info', 'manual_review']
    description: str = Field(min_length=1, max_length=2000)
    source: Source
    when: Condition | None = None
    check: Condition | None = None


class RouteRow(Strict):
    id: str = Field(max_length=64)
    description: str = Field(max_length=2000)
    when: Condition | None = None
    required_departments: list[str] = Field(max_length=16)
    conditional_departments: list['Department'] = Field(default_factory=list, max_length=16)
    source: Source


class Department(Strict):
    name: str = Field(max_length=255)
    condition: str = Field(max_length=1000)
    when: Condition | None = None


class Profile(Strict):
    profile_version: Literal[2]
    profile_id: str = Field(pattern=r'^[a-z][a-z0-9_-]{0,63}$')
    revision: str = Field(pattern=r'^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$')
    rules_version: str = Field(max_length=64)
    engine_version: Literal['docx-profile/2.0.0']
    template_kind: str = Field(max_length=64)
    source_sha256: str = Field(pattern=r'^[0-9a-f]{64}$')
    field_source: Source
    fields: list[ProfileField] = Field(min_length=1, max_length=64)
    bindings: list[Edit] = Field(max_length=128)
    conditional_blocks: list[Block] = Field(default_factory=list, max_length=16)
    repeaters: list[Repeater] = Field(default_factory=list, max_length=8)
    document_cleanup: Cleanup
    numbered_paragraphs: list[Anchor] = Field(default_factory=list, max_length=32)
    rules: list[Rule] = Field(max_length=128)
    approval_route: list[RouteRow] = Field(max_length=16)


def load_profile(raw):
    from app.templates.engine import TemplateError
    if len(raw.encode('utf-8')) > 196608:
        raise TemplateError('schema_too_large', 413)
    def unique(pairs):
        out = {}
        for k,v in pairs:
            if k in out:
                raise ValueError('duplicate')
            out[k] = v
        return out
    data = json.loads(raw, object_pairs_hook=unique)
    # Bound input nesting before recursive Pydantic validation.
    def bounded(node, depth=0):
        if depth > 24:
            raise ValueError('depth')
        if isinstance(node, dict):
            for v in node.values(): bounded(v, depth+1)
        elif isinstance(node, list):
            for v in node: bounded(v, depth+1)
    bounded(data)
    p = Profile.model_validate(data)
    keys = {f.key for f in p.fields}
    field_types = {f.key:f.type for f in p.fields}
    if len(keys) != len(p.fields) or len({r.id for r in p.rules}) != len(p.rules):
        raise ValueError('duplicate')
    for f in p.fields:
        if f.bindings or f.minimum_items > f.maximum_items:
            raise ValueError('field configuration')
        if (f.type == 'choice') != bool(f.options) or len(set(f.options)) != len(f.options):
            raise ValueError('choice configuration')
        if (f.type == 'repeater') != bool(f.item_fields) or len({i.key for i in f.item_fields}) != len(f.item_fields):
            raise ValueError('item configuration')
    budget = [0]
    def condition(c, allowed=keys, depth=0):
        if c is None: return set()
        budget[0] += 1
        if depth > 8 or budget[0] > 1024:
            raise ValueError('condition limit')
        if c.op in ('all', 'any', 'not'):
            if not c.args or c.op == 'not' and len(c.args) != 1 or c.field is not None or c.other_field is not None or c.value is not None:
                raise ValueError('logical condition')
            refs = set()
            for a in c.args: refs |= condition(a, allowed, depth+1)
            return refs
        if c.args or c.field not in allowed: raise ValueError('condition field')
        refs = {c.field}
        if c.op in ('field_equals', 'field_not_equals', 'date_gte', 'date_lte'):
            if c.other_field not in allowed or c.value is not None: raise ValueError('comparison')
            refs.add(c.other_field)
        elif c.other_field is not None: raise ValueError('comparison')
        if c.op == 'present' and c.value is not None: raise ValueError('present')
        if c.op == 'count_gt' and (type(c.value) is not int or c.value < 0 or c.value > 30): raise ValueError('count')
        if c.op == 'count_gt' and field_types.get(c.field,'text') not in ('text_list','person_list','repeater'): raise ValueError('count field type')
        if c.op in ('date_gte','date_lte') and (field_types.get(c.field)!='date' or field_types.get(c.other_field)!='date'): raise ValueError('date field type')
        if c.op in ('equals','not_equals') and (field_types.get(c.field,'text') in ('text_list','person_list','repeater') or not isinstance(c.value,str)): raise ValueError('scalar condition')
        if c.op in ('field_equals','field_not_equals') and field_types.get(c.field,'text')!=field_types.get(c.other_field,'text'): raise ValueError('comparison field types')
        return refs
    graph = {}
    for f in p.fields:
        graph[f.key] = condition(f.required_if) | condition(f.visible_if)
    completed = set()
    def visit(k, path):
        if k in path: raise ValueError('cyclic field dependencies')
        if k in completed: return
        for ref in graph[k]: visit(ref, path | {k})
        completed.add(k)
    for k in graph: visit(k, set())
    def segments(ss, allowed=keys, depth=0):
        if depth > 8: raise ValueError('segment depth')
        for s in ss:
            budget[0] += 1
            if budget[0] > 1024: raise ValueError('complexity')
            if sum((s.literal is not None, s.field is not None, s.when is not None)) != 1: raise ValueError('segment')
            if s.field is not None and s.field not in allowed: raise ValueError('segment field')
            if s.when is not None:
                condition(s.when, allowed)
                segments(s.then, allowed, depth+1); segments(s.otherwise, allowed, depth+1)
            elif s.then or s.otherwise: raise ValueError('segment branches')
    for b in p.bindings: segments(b.value)
    for b in p.conditional_blocks: condition(b.include_if)
    repeated = set()
    for r in p.repeaters:
        f = next((f for f in p.fields if f.key == r.field), None)
        if f is None or f.type != 'repeater' or r.field in repeated: raise ValueError('repeater field')
        repeated.add(r.field)
        for cell in r.cells: segments(cell, {i.key for i in f.item_fields})
    for r in p.rules: condition(r.when); condition(r.check)
    for r in p.approval_route:
        condition(r.when)
        for d in r.conditional_departments: condition(d.when)
    return p


def test_condition(c, values):
    if c is None: return True
    if c.op == 'all': return all(test_condition(a, values) for a in c.args)
    if c.op == 'any': return any(test_condition(a, values) for a in c.args)
    if c.op == 'not': return not test_condition(c.args[0], values)
    v = values.get(c.field)
    if c.op == 'present': return v is not None and v != '' and v != [] and (not isinstance(v, str) or bool(v.strip()))
    if c.op == 'equals': return type(v) is type(c.value) and v == c.value
    if c.op == 'not_equals': return v is not None and not (type(v) is type(c.value) and v == c.value)
    if c.op == 'count_gt': return isinstance(v, list) and len(v) > c.value
    other = values.get(c.other_field)
    if v is None or other is None: return False
    if c.op == 'field_equals': return type(v) is type(other) and v == other
    if c.op == 'field_not_equals': return type(v) is type(other) and v != other
    from datetime import date
    try:
        left, right = date.fromisoformat(v), date.fromisoformat(other)
        return left >= right if c.op == 'date_gte' else left <= right
    except (ValueError, TypeError): return False


def validate(p, values):
    from app.templates.engine import valid_text
    from datetime import date
    missing, invalid, visible, required, checks = [], [], [], [], []
    for f in p.fields:
        shown = test_condition(f.visible_if, values)
        needed = shown and (f.required or f.required_if is not None and test_condition(f.required_if, values))
        if shown: visible.append(f.key)
        if needed: required.append(f.key)
        v = values.get(f.key)
        empty = v is None or v == [] or isinstance(v, str) and not v.strip()
        valid = True
        if not empty:
            if f.type == 'repeater':
                valid = isinstance(v, list) and f.minimum_items <= len(v) <= f.maximum_items
                if valid:
                    for row in v:
                        if not isinstance(row, dict) or set(row) - {i.key for i in f.item_fields}: valid = False; break
                        for i in f.item_fields:
                            s = row.get(i.key, '')
                            if not valid_text(s, i.max_length) or i.required and not s.strip(): valid = False
            elif f.type in ('person_list', 'text_list'):
                valid = isinstance(v, list) and f.minimum_items <= len(v) <= f.maximum_items and all(valid_text(s, f.max_length) and s.strip() for s in v)
            else:
                valid = valid_text(v, f.max_length)
                if valid and f.type == 'choice': valid = v in f.options
                if valid and f.type == 'date':
                    try: valid = len(v) == 10 and date.fromisoformat(v).isoformat() == v
                    except ValueError: valid = False
        if not valid: invalid.append({'key': f.key, 'code': 'invalid_value'})
        elif needed and empty: missing.append(f.model_dump(exclude_none=True))
        if needed or not valid:
            checks.append(dict(rule_id='field.'+f.key, category='blocking', source=(f.source or p.field_source).model_dump(),
                               description='Заполнение и тип: '+f.label, result='failed' if not valid or empty else 'passed', method='typed_field'))
    for k in values:
        if k not in {f.key for f in p.fields}: invalid.append({'key': k, 'code': 'unknown_field'})
    for r in p.rules:
        active = test_condition(r.when, values)
        passed = r.check is not None and test_condition(r.check, values)
        result = 'not_applicable' if not active else 'user_confirmed' if passed and r.category == 'manual_review' else 'passed' if passed else 'failed' if r.category == 'blocking' else 'review_required' if r.category == 'manual_review' else 'attention'
        checks.append(dict(rule_id=r.id, category=r.category, source=r.source.model_dump(), description=r.description,
                           result=result, method='user_confirmation' if r.category == 'manual_review' and r.check else 'declarative' if r.check else 'manual'))
    ready = not missing and not invalid and not any(c['category']=='blocking' and c['result']=='failed' for c in checks)
    required_deps, rows, conditional = [], [], []
    for row in p.approval_route:
        active = test_condition(row.when, values)
        rows.append(dict(row_id=row.id, description=row.description, applicable=active,
                         applicability_condition=row.when.model_dump(exclude_none=True) if row.when else None,
                         required_departments=row.required_departments, source=row.source.model_dump()))
        if active:
            required_deps += row.required_departments
            for dep in row.conditional_departments:
                applies = dep.when is not None and test_condition(dep.when, values)
                conditional.append(dict(name=dep.name, condition=dep.condition, applicable=applies, row_id=row.id, source=row.source.model_dump()))
                if applies: required_deps.append(dep.name)
    route = dict(document_kind=p.template_kind, rows=rows, required_departments=list(dict.fromkeys(required_deps)),
                 conditional_departments=conditional, additional_departments=values.get('affected_departments', []),
                 status='recommendation', note='Дополнительно к общему маршруту; строки применяются совокупно. «Нет» не запрещает согласование. Согласование и подпись не выполнены.')
    return dict(ready=ready, missing_fields=missing, invalid_fields=invalid, next_question=missing[0] if missing else None,
                visible_fields=visible, required_fields=required, checks=checks, approval_route=route,
                profile_version=2, profile_id=p.profile_id, profile_revision=p.revision, rules_version=p.rules_version,
                engine_version='docx-profile/2.0.0')
