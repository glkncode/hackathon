"""Real public API, seven original DOCX files, persistent reports and v2 branches.

Synthetic fixtures are labelled and retained for inspection. No volumes are removed.
"""
import hashlib
import io
import json
import secrets
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from zipfile import ZipFile
from lxml import etree
from docx import Document
from dotenv import dotenv_values
from template_e2e import ROOT,MIME,request,session,health,compose
from template_v2_cases import cases,source_path

OUT=ROOT/'.local-integration/template-v2-e2e'
W='{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
def verify(p,v,content):
    with ZipFile(io.BytesIO(content)) as z,ZipFile(source_path(p)) as original:
        assert z.testzip() is None and set(z.namelist())==set(original.namelist())
        for name in z.namelist():
            if name.endswith(('.xml','.rels')): etree.fromstring(z.read(name))
            if name!='word/document.xml': assert z.read(name)==original.read(name)
        root=etree.fromstring(z.read('word/document.xml'))
        texts=[''.join(t.text or '' for t in n.iter(W+'t')) for n in root.iter(W+'p')]
        for a in p['document_cleanup']['protected']: assert texts.count(a['text'])==1
        alltext='\n'.join(texts)
        for t in ['МОДУЛЬНЫЙ ДОКУМЕНТ','Примечания по заполнению','Общие правила','УТВЕРЖДАЮ','[1]','[15]']:
            assert t not in alltext,t
        for r in p['repeaters']:
            rows=list(root.iter(W+'tbl'))[r['table']-1].findall(W+'tr')
            assert len(rows)==r['start_row']+len(v[r['field']])
            for item,row in zip(v[r['field']],rows[r['start_row']:]):
                assert item['name'] in ''.join(t.text or '' for t in row.iter(W+'t'))
        if 'projects' in v:
            assert all(alltext.count(name)==3 for name in v['projects'])
            assert ('об отмене' in alltext)==(v['repeal']=='yes')
        if 'system_short_name' in v:
            assert alltext.count(v['system_short_name'])==5+2*v['system_full_name'].count(v['system_short_name'])
        if 'self_control' in v:
            assert ('оставляю за собой' in alltext)==(v['self_control']=='yes')
        assert hashlib.sha256(source_path(p).read_bytes()).hexdigest()==p['source_sha256']
    Document(io.BytesIO(content))


def main():
    health(); OUT.mkdir(parents=True,exist_ok=True)
    env=dotenv_values(ROOT/'.env'); admin=session(env.get('ADMIN_USERNAME','admin'),env['ADMIN_PASSWORD'])
    suffix=secrets.token_hex(4)
    users=[]
    for i in range(2):
        password='Synthetic-'+secrets.token_hex(16); username=f'template_v2_{suffix}_{i}'
        user=request(admin,'POST','/api/v1/users',201,json=dict(username=username,password=password)).json()
        users.append((user,session(username,password)))
    (user,client),(_,foreign)=users
    reports=[]; persistent=[]
    for id,p,v in cases():
        original=source_path(p).read_bytes()
        doc=request(admin,'POST','/api/v1/documents',201,data={'title':f'E2E v2 {id} {suffix}'},files={'file':(source_path(p).name,original,MIME)}).json()
        payload=dict(document_id=doc['id'],document_version_id=doc['latest_version']['id'],name=f'E2E v2 {id} {suffix}',profile=p)
        if id=='working-group':
            plain={k:value for k,value in payload.items() if k!='profile'}
            response=request(admin,'POST','/api/v1/templates',400,json=plain)
            assert response.json()['support_status']=='needs_configuration'
        bad=deepcopy(payload); bad['profile']['source_sha256']='0'*64
        request(admin,'POST','/api/v1/templates',400,json=bad)
        if id=='working-group':
            bad=deepcopy(payload); bad['profile']['rules'][0]['check']=dict(op='exec',field='organization',value='x')
            request(admin,'POST','/api/v1/templates',400,json=bad)
            bad=deepcopy(payload); bad['profile']['bindings'][0]['paragraph']=99999
            request(admin,'POST','/api/v1/templates',400,json=bad)
        template=request(admin,'POST','/api/v1/templates',201,json=payload).json()
        assert template['schema_version']==2 and template['profile']==p
        access=f"/api/v1/documents/{doc['id']}/access"
        request(admin,'POST',access,201,json={'user_id':user['id']})
        create=f"/api/v1/templates/{template['id']}/drafts"
        request(foreign,'POST',create,404)
        draft=request(client,'POST',create,201).json(); path=f"/api/v1/template-drafts/{draft['id']}"
        request(foreign,'GET',path,404); request(admin,'GET',path,404)
        request(client,'POST',path+'/generate',422,json={'expected_revision':1})
        request(client,'PATCH',path,400,json={'expected_revision':1,'values':{'unknown':'x'}})
        # Follow conditional questions on committee; do not inject hidden repeal answers.
        if id=='project-committee':
            v={k:val for k,val in v.items() if not k.startswith('old_act_')}
            while True:
                validation=request(client,'GET',path+'/next-question').json()
                if validation['ready']: break
                key=validation['next_question']['key']; assert not key.startswith('old_act_')
                draft=request(client,'POST',path+'/answers',json={'expected_revision':draft['revision'],'field_key':key,'value':v[key]}).json()
            draft=request(client,'PATCH',path,json={'expected_revision':draft['revision'],'values':{'repeal':'yes'}}).json()
            validation=request(client,'POST',path+'/validate').json()
            assert not validation['ready'] and validation['next_question']['key']=='old_act_type'
            request(client,'POST',path+'/generate',422,json={'expected_revision':draft['revision']})
            draft=request(client,'PATCH',path,json={'expected_revision':draft['revision'],'values':{'repeal':'no'}}).json()
        else:
            draft=request(client,'PATCH',path,json={'expected_revision':draft['revision'],'values':v}).json()
        if id=='is-commissioning':
            draft=request(client,'PATCH',path,json={'expected_revision':draft['revision'],'values':{'commissioning_date':'2026-11-30'}}).json()
            validation=request(client,'POST',path+'/validate').json(); assert not validation['ready']
            assert any(c['rule_id']=='is.date' and c['result']=='failed' for c in validation['checks'])
            request(client,'POST',path+'/generate',422,json={'expected_revision':draft['revision']})
            request(client,'PATCH',path,400,json={'expected_revision':draft['revision'],'values':{'readiness_date':'2026-02-30'}})
            draft=request(client,'PATCH',path,json={'expected_revision':draft['revision'],'values':{'commissioning_date':v['commissioning_date']}}).json()
        validation=request(client,'POST',path+'/validate').json(); assert validation['ready'],validation
        rev=draft['revision']
        if id=='working-group':
            def generate(_): return request(client,'POST',path+'/generate',json={'expected_revision':rev}).json()
            with ThreadPoolExecutor(max_workers=2) as executor: generated=list(executor.map(generate,range(2)))
            assert generated[0]['generated_document_id']==generated[1]['generated_document_id']
            g=generated[0]
        else: g=request(client,'POST',path+'/generate',json={'expected_revision':rev}).json()
        request(client,'PATCH',path,409,json={'expected_revision':1,'values':{'organization':'stale'}})
        metadata=request(client,'GET','/api/v1/generated-documents/'+g['generated_document_id']).json()
        snapshot=metadata['generation_snapshot']; assert snapshot==g['generation_snapshot']
        assert snapshot['profile']==p and snapshot['validation']['ready'] and snapshot['engine_version']=='docx-profile/2.0.0'
        assert snapshot['values']==draft['values']
        assert any(c['rule_id']=='structure.output' and c['result']=='passed' for c in snapshot['validation']['checks'])
        output=request(client,'GET',g['download_url']).content; verify(p,v,output)
        (OUT/(id+'.docx')).write_bytes(output)
        (OUT/(id+'-report.json')).write_text(json.dumps(snapshot,ensure_ascii=False,indent=2),encoding='utf-8')
        request(foreign,'GET',g['download_url'],404)
        persistent.append((g,hashlib.sha256(output).hexdigest(),access,path,doc['id']))
        # Alternate branches through the same public API and a new immutable result revision.
        other=deepcopy(v)
        for key in ['repeal','self_control','include_source','deadline_in_basis']:
            if key in other: other[key]='yes'
        if 'repeal' in other:
            old_type=next(f for f in p['fields'] if f['key']=='old_act_type')['options'][0]
            other.update(old_act_type=old_type,old_act_date='2026-09-01',old_act_number='Т-9',old_act_name='О создании проектного комитета' if id=='project-committee' else 'Об организации работ')
        for key in ['projects','processes']:
            if key in other: other[key]=other[key][:1]
        if 'change_kind' in other: other['change_kind']='modified'
        draft=request(client,'PATCH',path,json={'expected_revision':rev,'values':other}).json()
        g2=request(client,'POST',path+'/generate',json={'expected_revision':draft['revision']}).json()
        assert g2['generated_document_id']!=g['generated_document_id']
        out2=request(client,'GET',g2['download_url']).content; verify(p,other,out2)
        (OUT/(id+'-alternate.docx')).write_bytes(out2)
        assert hashlib.sha256(request(client,'GET',g['download_url']).content).hexdigest()==persistent[-1][1]
        reports.append(dict(module=id,source_sha256=p['source_sha256'],template_id=template['id'],draft_id=draft['id'],generated_id=g['generated_document_id'],alternate_generated_id=g2['generated_document_id'],status='passed'))
        print('PASS: '+id+' — public API, conditional branches, original DOCX, report, ACL, revision pin',flush=True)
    if '--restart' in sys.argv:
        compose('restart','api','ai-service'); health()
        admin=session(env.get('ADMIN_USERNAME','admin'),env['ADMIN_PASSWORD'])
        for g,sha,access,path,docid in persistent:
            assert hashlib.sha256(request(client,'GET',g['download_url']).content).hexdigest()==sha
            assert request(client,'GET',path).json()['profile']['profile_version']==2
        compose('stop','ai-service')
        try:
            for g,sha,*_ in persistent:
                assert hashlib.sha256(request(client,'GET',g['download_url']).content).hexdigest()==sha
            print('PASS: v2 downloads and persisted reports survive renderer outage',flush=True)
        finally: compose('start','ai-service')
    for g,sha,access,path,docid in persistent:
        request(admin,'DELETE',access+'/'+user['id'],204)
        request(client,'GET',g['download_url'],404); request(client,'GET',path,404)
        request(admin,'POST',access,201,json={'user_id':user['id']})
    (OUT/'verification.json').write_text(json.dumps(reports,ensure_ascii=False,indent=2),encoding='utf-8')
    print('PASS: all seven originals, 14 generated projects, source checksums, ACL revocation and persistence',flush=True)


if __name__=='__main__': main()
