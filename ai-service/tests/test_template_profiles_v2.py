"""Real-original structural tests, including both branches of every reviewed profile."""
import hashlib
import io
import json
import sys
from copy import deepcopy
from pathlib import Path
from zipfile import ZipFile
import pytest
from docx import Document
from lxml import etree

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'scripts'))
from template_v2_cases import cases,source_path
from app.templates.engine import process_template,TemplateError,W
from app.templates.profile import load_profile

CASES=list(cases())
def call(p,v,render=False,content=None):
    if content is None: content=source_path(p).read_bytes()
    return process_template(content,json.dumps(v,ensure_ascii=False),render,json.dumps(p,ensure_ascii=False))
def text(root): return ''.join(t.text or '' for t in root.iter(W+'t'))


def verify(p,values,result):
    original=source_path(p).read_bytes()
    assert hashlib.sha256(original).hexdigest()==p['source_sha256']
    with ZipFile(io.BytesIO(original)) as a,ZipFile(io.BytesIO(result)) as b:
        assert b.testzip() is None and set(a.namelist())==set(b.namelist())
        for name in b.namelist():
            if name.endswith(('.xml','.rels')): etree.fromstring(b.read(name))
            if name!='word/document.xml': assert a.read(name)==b.read(name),name
        old=etree.fromstring(a.read('word/document.xml')); new=etree.fromstring(b.read('word/document.xml'))
        for anchor in p['document_cleanup']['protected']:
            node=list(old.iter(W+'p'))[anchor['paragraph']]
            matches=[n for n in new.iter(W+'p') if text(n)==anchor['text']]
            assert len(matches)==1 and etree.tostring(node)==etree.tostring(matches[0])
        assert etree.tostring(old.find('.//'+W+'sectPr'))==etree.tostring(new.find('.//'+W+'sectPr'))
        joined=text(new)
        for unwanted in ['МОДУЛЬНЫЙ ДОКУМЕНТ','УТВЕРЖДАЮ','Примечания по заполнению','Общие правила','[1]','[15]','[проекту / проектам]','процесса / процессов','разработанного / измененного']:
            assert unwanted not in joined,unwanted
        for n in new.iter(W+'p'):
            s=text(n)
            if 'НЕ УДАЛЯТЬ' not in s and s!='______________': assert '___' not in s,s
        for r in p['repeaters']:
            table=list(new.iter(W+'tbl'))[r['table']-1] # only approval table removed
            rows=table.findall(W+'tr')
            assert len(rows)==r['start_row']+len(values[r['field']])
            prototype=list(old.iter(W+'tbl'))[r['table']].findall(W+'tr')[r['start_row']]
            for row,item in zip(rows[r['start_row']:],values[r['field']]):
                assert item['name'] in text(row)
                assert row.find(W+'trPr/'+W+'cantSplit') is not None
                for prop in ['tcPr','pPr','rPr']:
                    assert [etree.tostring(n) for n in row.iter(W+prop)]==[etree.tostring(n) for n in prototype.iter(W+prop)]
        nums=[text(n).split('.')[0] for n in new.find(W+'body').findall(W+'p') if text(n)[:1].isdigit() and '. ' in text(n)]
        if p['numbered_paragraphs']: assert nums==list(map(str,range(1,len(nums)+1)))
    Document(io.BytesIO(result))
    return new


@pytest.mark.parametrize('id,p,values',CASES,ids=[c[0] for c in CASES])
def test_original_profiles_branches(id,p,values):
    assert call(p,values)['can_render']
    verify(p,values,call(p,values,True))
    other=deepcopy(values)
    for key in ['repeal','self_control','include_source','deadline_in_basis']:
        if key in other: other[key]='yes'
    if 'projects' in other: other['projects']=other['projects'][:1]
    if 'processes' in other: other['processes']=other['processes'][:1]
    if 'change_kind' in other: other['change_kind']='modified'
    if 'transition' in other: other['transition']='Переходный период до 01.02.2027.'
    if 'it_related' in other: other['it_related']='no'
    assert call(p,other)['can_render']
    verify(p,other,call(p,other,True))


def test_user_text_is_never_pattern_deleted():
    _,p,v=next(c for c in CASES if c[0]=='working-group')
    v=deepcopy(v); v['purpose']='Примечания по заполнению — пользовательский текст [42]'
    assert v['purpose'] in text(etree.fromstring(ZipFile(io.BytesIO(call(p,v,True))).read('word/document.xml')))


@pytest.mark.parametrize('id,p,values',CASES,ids=[c[0] for c in CASES])
def test_missing_unknown_invalid_and_pinning(id,p,values):
    missing=call(p,{})['validation']
    assert not missing['ready'] and missing['next_question'] and missing['checks']
    with pytest.raises(TemplateError): call(p,{},True)
    invalid=call(p,dict(values,unknown='x'))['validation']
    assert invalid['invalid_fields']==[dict(key='unknown',code='unknown_field')]
    tampered=source_path(p).read_bytes()+b'x'
    with pytest.raises(TemplateError,match='profile_source_mismatch'): call(p,values,content=tampered)
    wrong=deepcopy(p); wrong['bindings'][0]['paragraph']=99999
    with pytest.raises(TemplateError,match='profile_anchor_mismatch'): call(wrong,values)
    wrong=deepcopy(p); wrong['rules'][0]['check']={'op':'exec','field':'organization','value':'unsafe'}
    with pytest.raises(TemplateError): call(wrong,values)
    assert call(p,dict(values,organization=1))['validation']['invalid_fields']


def test_dates_and_substantiated_exception():
    _,p,v=next(c for c in CASES if c[0]=='is-commissioning'); v=deepcopy(v)
    v['commissioning_date']='2026-11-30'
    assert not call(p,v)['can_render']
    v['date_exception']='yes'; v['date_exception_basis']=''
    assert not call(p,v)['can_render']
    v['date_exception_basis']='Положение о вводе, пункт 3, приказ от 01.10.2026 № Т-3 (сведения пользователя)'
    report=call(p,v)['validation']; assert report['ready']
    assert any(c['rule_id']=='is.date_exception' and c['result']=='review_required' for c in report['checks'])
    v['readiness_date']='2026-02-30'
    assert call(p,v)['validation']['invalid_fields']
    with pytest.raises(TemplateError): call(p,v,True)


def test_repeal_and_conditional_questions():
    _,p,v=next(c for c in CASES if c[0]=='project-committee'); v=deepcopy(v)
    for k in list(v):
        if k.startswith('old_act_'): v.pop(k)
    assert call(p,v)['can_render']
    assert 'old_act_name' not in call(p,v)['validation']['visible_fields']
    v['repeal']='yes'
    report=call(p,v)['validation']
    assert not report['ready'] and report['next_question']['key']=='old_act_type'
    with pytest.raises(TemplateError): call(p,v,True)


def test_repeater_limits_and_actual_cell_mapping():
    _,p,v=next(c for c in CASES if c[0]=='records-responsibles'); v=deepcopy(v)
    for count in [0,31]:
        v['responsibles']=[dict(unit='ДОУ',position='Специалист',name='Тестов Т.Т.')] * count
        assert not call(p,v)['can_render']
    v['responsibles']=[dict(unit='ДОУ',position='Специалист')]
    assert not call(p,v)['can_render']


def test_matrix_actual_rows():
    for id,p,v in CASES:
        route=call(p,v)['validation']['approval_route']
        assert route['status']=='recommendation' and 'Департамент ДОУ' in route['required_departments']
        if id in ['working-group','project-committee']:
            assert 'Кадровое подразделение' in route['required_departments']
            assert 'Юридическое подразделение' not in route['required_departments']
        if id=='is-commissioning': assert 'Департамент информационной безопасности' in route['required_departments']
        for row in route['rows']: assert row['source']['version'].startswith('sha256:')


def test_cycles_depth_complexity_and_duplicate_json():
    _,p,v=CASES[0]; p=deepcopy(p)
    a,b=p['fields'][:2]
    a['visible_if']=dict(op='present',field=b['key']); b['required_if']=dict(op='present',field=a['key'])
    with pytest.raises(ValueError,match='cyclic'): load_profile(json.dumps(p))
    _,p,v=CASES[0]; p=deepcopy(p)
    c=dict(op='present',field='organization')
    for _ in range(10): c=dict(op='not',args=[c])
    p['rules'][0]['check']=c
    with pytest.raises(ValueError): load_profile(json.dumps(p))
    raw=json.dumps(p).replace('"profile_version": 2','"profile_version": 2, "profile_version": 2')
    with pytest.raises(ValueError): load_profile(raw)


def test_repeated_name_equality_is_typed_and_source_backed():
    _,p,v=next(c for c in CASES if c[0]=='working-group');p=deepcopy(p);v=deepcopy(v)
    # The shipped profile reuses one field in all locations. A configurable equality
    # rule is also supported when an administrator explicitly models separate inputs.
    p['rules'].append(dict(id='test.names',category='blocking',description='Повторяемые названия должны совпадать',
                          source=dict(p['field_source'],section='Примечание 2'),
                          check=dict(op='field_equals',field='group_subject',other_field='purpose')))
    assert not call(p,v)['can_render']
    v['purpose']=v['group_subject']
    assert call(p,v)['can_render']


def test_multi_paragraph_ranges_and_protected_anchors():
    _,p,v=next(c for c in CASES if c[0]=='project-committee');p=deepcopy(p)
    p['conditional_blocks'][0]['end']=p['numbered_paragraphs'][-1]
    result=call(p,v,True)
    root=etree.fromstring(ZipFile(io.BytesIO(result)).read('word/document.xml'))
    assert 'Контроль за исполнением' not in text(root)
    assert 'Комитет) в составе' in text(root)
    p['conditional_blocks'][0]['end']=p['document_cleanup']['protected'][-1]
    with pytest.raises(TemplateError):call(p,v)


def test_local_document_type_declension_and_repeated_name():
    _,p,v=next(c for c in CASES if c[0]=='local-document');v=deepcopy(v);v['document_type']='Инструкция'
    root=etree.fromstring(ZipFile(io.BytesIO(call(p,v,True))).read('word/document.xml'))
    value=text(root)
    assert 'Об утверждении и введении в действие Инструкции' in value
    assert '1. Утвердить Инструкцию' in value
    assert value.count(v['document_name'])==2


def test_source_fingerprint_cannot_opt_out_of_safety():
    _,p,v=next(c for c in CASES if c[0]=='working-group');p=deepcopy(p)
    p['document_cleanup']['protected']=p['numbered_paragraphs'] or [dict(paragraph=8,text='НАИМЕНОВАНИЕ ОРГАНИЗАЦИИ')]*2
    with pytest.raises(TemplateError,match='protected_block_configuration'):call(p,v)


def test_committee_repeal_type_and_connected_heading_case():
    _,p,v=next(c for c in CASES if c[0]=='project-committee');v=deepcopy(v);v['repeal']='yes'
    v['old_act_type']='приказ Организации'
    assert not call(p,v)['can_render']
    v['old_act_type']='распоряжение Организации'
    root=etree.fromstring(ZipFile(io.BytesIO(call(p,v,True))).read('word/document.xml'))
    value=text(root)
    assert 'и об отмене распоряжения Организации от' in value
    assert '2. Отменить распоряжение Организации от' in value
    assert '3. Контроль за исполнением' in value
