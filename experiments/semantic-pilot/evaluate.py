"""Opt-in local pilot. Real Go Search/Chat + pgvector + existing LAN Qwen.

Only the explicitly named disposable containers/DB are used. Synthetic artifacts
contain no token or inference address. Successful observations are never rerun.
"""
import argparse
import hashlib
import json
import subprocess
import shutil
import sys
import threading
import time
from pathlib import Path
from uuid import UUID, uuid4

import httpx
import numpy as np

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
sys.path.insert(0, str(ROOT / 'experiments/rag'))
import benchmark as old
from embedding import metrics

OUT = ROOT / '.local-integration/semantic-pilot'
BASES = {'fts':'http://127.0.0.1:18091', 'semantic':'http://127.0.0.1:18092', 'hybrid':'http://127.0.0.1:18093'}
PASSWORD = 'semantic-pilot-test-only-123'
FINAL = False


def save(name, value):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / name).write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')


def load(name):
    return json.loads((OUT / name).read_text(encoding='utf-8'))


def sql(statement):
    result = subprocess.run(['docker','exec','-i','hackathon-semantic-pg','psql','-X','-v','ON_ERROR_STOP=1','-U','search','-d','semantic_test','-At'],
                            input=statement, encoding='utf-8', capture_output=True, check=True)
    return json.loads(result.stdout.strip())


old.OUT = OUT
old.BASE = BASES['fts']
old.PASSWORD = PASSWORD
old.sql = sql


class API:
    def __init__(self, method, username='rag-experiment'):
        base = BASES[method]
        self.http = httpx.Client(base_url=base, headers={'Origin':base},timeout=100,trust_env=False)
        self.user = self.request('POST','/api/v1/auth/login',json={'username':username,'password':PASSWORD})['user']
        self.http.headers['X-CSRF-Token'] = self.http.cookies['ai_csrf']

    def request(self, method, path, **kwargs):
        response = self.http.request(method,path,**kwargs)
        response.raise_for_status()
        return response.json() if response.content else None

    def search(self, query):
        return self.request('POST','/api/v1/search',json={'query':query,'limit':10})['results']

    def close(self):
        self.request('POST','/api/v1/auth/logout')
        self.http.close()


def prepare():
    if (OUT / 'manifest.json').exists():
        raise RuntimeError('Manifest already frozen')
    independent = json.loads((HERE/'corpus.json').read_text(encoding='utf-8'))
    regression = json.loads((ROOT/'experiments/rag/corpus.json').read_text(encoding='utf-8'))
    if FINAL:
        initial=independent
        for d in initial['documents']:d['split']='diagnostic'
        for q in initial['negatives']:q['split']='diagnostic'
        regression['documents']+=initial['documents']
        regression['negatives']+=initial['negatives']
        independent=json.loads((HERE/'corpus-final.json').read_text(encoding='utf-8'))
        OUT.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(ROOT/'.local-integration/semantic-pilot/prepare-state.json',OUT/'prepare-state.json')
    combined = {'revision':'pilot-frozen-v1','documents':regression['documents']+independent['documents'],
                'negatives':regression['negatives']+independent['negatives']}
    save('corpus.json',combined)
    # Reuse the previously tested upload, parser fixtures, real IDs and gold mapping.
    old.HERE = OUT
    old.prepare()
    wait_index()


def wait_index():
    started = time.perf_counter()
    while time.perf_counter()-started < 300:
        counts = sql("SELECT json_build_object('ready',count(*) FILTER(WHERE s.status='ready'),'all',count(*)) FROM documents d JOIN document_versions v ON v.id=d.active_version_id LEFT JOIN semantic_indexes s ON s.document_version_id=v.id AND s.model='intfloat/multilingual-e5-small' AND s.revision='614241f622f53c4eeff9890bdc4f31cfecc418b3' WHERE v.status='ready'")
        if counts['all'] and counts['all']==counts['ready']:
            save('indexing.json',{'wait_after_upload_seconds':time.perf_counter()-started,'counts':counts,
                                 'indexes':sql("SELECT json_agg(x) FROM (SELECT document_version_id,status,attempts,created_at,completed_at,extract(epoch FROM completed_at-created_at) AS queue_to_ready_seconds FROM semantic_indexes) x"),
                                 'windows':sql('SELECT count(*) FROM embedding_windows')})
            print('semantic indexes ready',counts,flush=True)
            return
        time.sleep(1)
    raise RuntimeError('Semantic indexing timeout')


def service_token():
    data = json.loads(subprocess.check_output(['docker','inspect','hackathon-semantic-ai']))[0]
    return next(v.split('=',1)[1] for v in data['Config']['Env'] if v.startswith('AI_SERVICE_TOKEN='))


class Resources:
    def __enter__(self):
        self.samples=[];self.stop=threading.Event()
        def sample():
            while not self.stop.is_set():
                lines=subprocess.check_output(['docker','stats','--no-stream','--format','{{json .}}','hackathon-semantic-ai','hackathon-semantic-pg','hackathon-semantic-api-semantic'],text=True)
                self.samples.extend(json.loads(line) for line in lines.splitlines())
                self.stop.wait(3)
        self.thread=threading.Thread(target=sample,daemon=True);self.thread.start();return self
    def __exit__(self,*args):
        self.stop.set();self.thread.join();save('resources-'+str(int(time.time()))+'.json',self.samples)


def raw_semantic(user, vector):
    user=str(UUID(user))
    v=json.dumps(vector,allow_nan=False,separators=(',',':'))
    return sql(f"""SELECT coalesce(json_agg(x ORDER BY score DESC,chunk_id),'[]') FROM (
        SELECT c.id AS chunk_id,1-min(w.embedding OPERATOR(public.<=>) '{v}'::public.vector) AS score
        FROM document_chunks c JOIN document_versions dv ON dv.id=c.document_version_id
        JOIN documents d ON d.id=dv.document_id AND d.active_version_id=dv.id
        JOIN users u ON u.id='{user}' AND u.is_active AND u.role='member'
        JOIN embedding_windows w ON w.chunk_id=c.id AND w.document_version_id=dv.id
        JOIN semantic_indexes s ON s.id=w.index_id AND s.status='ready'
        WHERE dv.status='ready' AND s.model='intfloat/multilingual-e5-small'
        AND s.revision='614241f622f53c4eeff9890bdc4f31cfecc418b3'
        AND s.format_revision='e5-exact-prefix-v1-title-content-512-overlap64-mean-l2-max'
        AND s.dimensions=384 AND EXISTS(SELECT 1 FROM document_access a WHERE a.document_id=d.id AND a.user_id=u.id)
        GROUP BY c.id ORDER BY score DESC,c.id LIMIT 10) x""")


def compare():
    manifest=load('manifest.json');allowed={c['chunk_id'] for c in manifest['chunks']}
    rows=load('retrieval.json') if (OUT/'retrieval.json').exists() else []
    done={r['query_id'] for r in rows}
    questions=[q for q in manifest['questions'] if q['split'] in {'independent','holdout'}]
    apis={m:API(m) for m in BASES}
    token=service_token()
    with Resources(),httpx.Client(trust_env=False,timeout=45) as ai:
        for q in questions:
            if q['id'] in done:continue
            observation={'query_id':q['id'],'split':q['split'],'query':q['query'],'gold_chunk_ids':q['gold_chunk_ids'],'methods':{}}
            for method,api in apis.items():
                start=time.perf_counter();results=api.search(q['query']);elapsed=(time.perf_counter()-start)*1000
                assert {s['chunk_id'] for s in results}<=allowed
                assert all('storage_key' not in s for s in results)
                observation['methods'][method]={'results':[(s['chunk_id'],s['score']) for s in results],'latency_ms':elapsed}
            start=time.perf_counter()
            response=ai.post('http://127.0.0.1:18094/internal/v1/embeddings',json={'kind':'query','inputs':[q['query']]},headers={'X-Service-Token':token})
            response.raise_for_status();raw=raw_semantic(manifest['member_id'],response.json()['embeddings'][0])
            observation['methods']['raw_semantic']={'results':[(s['chunk_id'],s['score']) for s in raw], 'latency_ms':(time.perf_counter()-start)*1000}
            raw_fused=list(observation['methods']['raw_semantic']['results']);seen={r[0] for r in raw_fused}
            for r in observation['methods']['fts']['results']:
                if r[0] not in seen:raw_fused.append(r);seen.add(r[0])
            observation['methods']['raw_hybrid']={'results':raw_fused[:10], 'latency_ms':observation['methods']['raw_semantic']['latency_ms']+observation['methods']['fts']['latency_ms']}
            rows.append(observation);save('retrieval.json',rows)
            print('retrieved',q['id'],{m:len(r['results']) for m,r in observation['methods'].items()},flush=True)
    for api in apis.values():api.close()
    summary={}
    for split in ['independent','holdout']:
        summary[split]={m:metrics([{**r,**r['methods'][m]} for r in rows if r['split']==split]) for m in rows[0]['methods']}
    save('retrieval-summary.json',summary)
    print(json.dumps(summary,ensure_ascii=False,indent=2))


def answers():
    manifest=load('manifest.json');allowed={c['chunk_id'] for c in manifest['chunks']}
    rows=load('answers.json') if (OUT/'answers.json').exists() else []
    done={(r['method'],r['query_id']) for r in rows}
    with Resources():
        for method in BASES:
            api=API(method)
            for q in manifest['questions']:
                if q['split']!='independent' or (method,q['id']) in done:continue
                session=api.request('POST','/api/v1/chat/sessions',json={'title':'Synthetic independent evaluation'})
                path='/api/v1/chat/sessions/'+session['id']
                start=time.perf_counter()
                turn=api.request('POST',path+'/messages',json={'content':q['query'],'client_message_id':str(uuid4())})
                ids=[s['chunk_id'] for s in turn['sources']]
                assert set(ids)<=allowed and len(ids)==len(set(ids))
                rows.append({'method':method,'query_id':q['id'],'query':q['query'],'gold_chunk_ids':q['gold_chunk_ids'],
                             'mode':turn['mode'],'answer':turn['message'],'used_source_ids':ids,'session_id':session['id'],
                             'elapsed_ms':(time.perf_counter()-start)*1000})
                save('answers.json',rows)
                print('answered',method,q['id'],turn['mode'],len(ids),flush=True)
            api.close()


def verify():
    manifest=load('manifest.json');api=API('semantic');admin=API('fts','admin')
    key='translations' if FINAL else 'digitization'
    doc=manifest['documents'][key];mid=manifest['member_id']
    q='При обнаружении более пяти терминологических ошибок' if FINAL else 'Качество цифровой копии'
    before=api.search(q);assert any(s['document_id']==doc['id'] for s in before)
    admin.request('DELETE',f"/api/v1/documents/{doc['id']}/access/{mid}")
    try:
        assert not any(s['document_id']==doc['id'] for s in api.search(q))
        for r in load('answers.json'):
            if r['method']=='semantic' and r['query_id']==('f05' if FINAL else 'i01'):
                messages=api.request('GET','/api/v1/chat/sessions/'+r['session_id'])['messages']
                assert any(m['content']=='Содержимое ответа недоступно из-за изменения прав доступа к документам.' for m in messages if m['role']=='assistant')
    finally:admin.request('POST',f"/api/v1/documents/{doc['id']}/access",json={'user_id':mid})
    ids=sql('SELECT json_agg(chunk_id ORDER BY chunk_id) FROM embedding_windows')
    subprocess.run(['docker','stop','hackathon-semantic-ai'],check=True,capture_output=True)
    try:
        # Literal positive control exercises unchanged FTS with Python stopped.
        fallback=api.search('гигрометры');assert fallback
        session=api.request('POST','/api/v1/chat/sessions',json={'title':'Outage check'})
        turn=api.request('POST','/api/v1/chat/sessions/'+session['id']+'/messages',json={'content':'гигрометры','client_message_id':str(uuid4())})
        assert turn['mode']=='search'
    finally:subprocess.run(['docker','start','hackathon-semantic-ai'],check=True,capture_output=True)
    # Fixed pilot containers only; never stop the original app or remove data.
    subprocess.run(['docker','restart','hackathon-semantic-pg','hackathon-semantic-api-semantic'],check=True,capture_output=True)
    for _ in range(60):
        try:
            if sql('SELECT json_agg(chunk_id ORDER BY chunk_id) FROM embedding_windows')==ids:break
        except subprocess.CalledProcessError:pass
        time.sleep(.5)
    else:raise RuntimeError('Persistence check failed')
    save('security.json',{'acl_revoke':True,'history_redaction':True,'fts_with_python_stopped':True,'chat_with_python_stopped':True,'vectors_survive_restart':True})
    api.close();admin.close()
    print('security and restart checks passed')


def probe():
    if (OUT/'long-context-probe.json').exists():raise RuntimeError('Probe already recorded')
    manifest=load('manifest.json');admin=API('fts','admin');member=API('semantic')
    content='1. '+'абвгдежзйклмнопрстуфхцчшщ '*125+' Контрольный образец в процедуре длинной записи хранится ровно семнадцать суток.'
    doc=admin.request('POST','/api/v1/documents',data={'title':'Синтетическая процедура длинной записи'},files={'file':('long-probe.txt',content.encode('utf-8'),'text/plain')})
    version=str(UUID(doc['latest_version']['id']));old.wait_ready(version)
    admin.request('POST',f"/api/v1/documents/{doc['id']}/access",json={'user_id':manifest['member_id']})
    for _ in range(90):
        state=sql(f"SELECT row_to_json(s) FROM (SELECT status FROM semantic_indexes WHERE document_version_id='{version}') s")
        if state and state['status']=='ready':break
        time.sleep(.5)
    else:raise RuntimeError('Long probe index not ready')
    windows=sql(f"SELECT json_agg(x ORDER BY chunk_id,window_index) FROM (SELECT chunk_id,window_index,start_token,end_token,public.vector_dims(embedding) AS dimensions,public.vector_norm(embedding) AS norm FROM embedding_windows WHERE document_version_id='{version}') x")
    chunks=sql(f"SELECT json_agg(x) FROM (SELECT c.id,sc.total_tokens,sc.window_count FROM document_chunks c JOIN semantic_chunks sc ON sc.chunk_id=c.id WHERE c.document_version_id='{version}') x")
    assert any(c['window_count']>1 for c in chunks)
    for c in chunks:
        rows=[w for w in windows if w['chunk_id']==c['id']]
        assert len(rows)==c['window_count'] and rows[0]['start_token']==0 and rows[-1]['end_token']==c['total_tokens']
        assert all(r['dimensions']==384 and abs(r['norm']-1)<.001 for r in rows)
        assert all(rows[i]['start_token']==rows[i-1]['end_token']-64 for i in range(1,len(rows)))
    results=member.search('Какой срок хранения контрольного образца установлен в процедуре длинной записи?')
    assert any(r['document_version_id']==version and 'семнадцать суток' in r['content'] for r in results)
    save('long-context-probe.json',{'document_id':doc['id'],'version_id':version,'chunks':chunks,'windows':windows,'tail_found_through_go_semantic_search':True,'measured_after_frozen_quality_evaluation':True})
    admin.close();member.close();print('long-context ingestion/index/search probe passed',len(windows),'windows',flush=True)


def export():
    destination=HERE/('results/2026-10-24-final' if FINAL else 'results/2026-10-24');destination.mkdir(parents=True,exist_ok=True)
    names=['manifest.json','retrieval.json','retrieval-summary.json','answers.json','indexing.json','security.json']
    names += [p.name for p in OUT.glob('resources-*.json')]
    token=service_token()
    for name in names:
        raw=(OUT/name).read_text(encoding='utf-8')
        if token in raw:raise RuntimeError('Private token in artifact')
        (destination/name).write_text(raw,encoding='utf-8')
    print('synthetic artifacts exported')


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('stage',choices=['prepare','wait_index','compare','answers','verify','probe','export'])
    parser.add_argument('--final',action='store_true')
    args=parser.parse_args()
    if args.final:
        FINAL=True
        OUT=ROOT/'.local-integration/semantic-final'
        old.OUT=OUT
    globals()[args.stage]()
