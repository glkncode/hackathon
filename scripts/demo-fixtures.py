"""Idempotent local demo setup through existing public APIs only.

Uses synthetic member accounts, labelled demo documents and existing labelled
E2E v2 templates. Never overwrites source files, existing users or documents.
Run with the existing ai-service/.venv/Scripts/python.exe.
"""
import argparse
import hashlib
import json
from pathlib import Path
import secrets
import subprocess
import threading
import time
from uuid import UUID, uuid4

from dotenv import dotenv_values
from template_e2e import ROOT, request, session
from template_v2_cases import cases
from template_v2_e2e import verify
from template_v2_cases import source_path

OUT = ROOT / '.local-integration/final-demo'
MANIFEST = OUT / 'manifest.json'
SECRETS = OUT / 'credentials.json'


def save(path, value):
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')
    temporary.replace(path)


def load(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def admin_client():
    env = dotenv_values(ROOT / '.env')
    return session(env.get('ADMIN_USERNAME', 'admin'), env['ADMIN_PASSWORD'])


def list_all(client, path):
    items, offset = [], 0
    while True:
        page = request(client, 'GET', f'{path}?limit=100&offset={offset}').json()['items'] or []
        items.extend(page)
        if len(page)<100:
            return items
        offset += 100


def clients():
    credentials = load(SECRETS)
    return [session(credentials[key]['username'], credentials[key]['password'])
            for key in ('demo', 'foreign')]


def sql(statement):
    p = subprocess.run(['docker', 'compose', 'exec', '-T', 'postgres', 'sh', '-c',
                        'psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'],
                       input='BEGIN READ ONLY;\n' + statement + '\nCOMMIT;',
                       cwd=ROOT, text=True, encoding='utf-8', capture_output=True, timeout=30)
    if p.returncode:
        raise RuntimeError('Read-only database probe failed')
    return json.loads(p.stdout)


def wait_indexes(manifest, timeout=240):
    ids = ','.join("'" + str(UUID(x['id'])) + "'"
                   for x in manifest['documents'].values())
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        rows = sql("SELECT coalesce(json_agg(t),'[]') FROM (SELECT d.id,v.status,"
                   "s.status index_status FROM documents d LEFT JOIN document_versions v "
                   "ON v.id=d.active_version_id LEFT JOIN semantic_indexes s "
                   "ON s.document_version_id=v.id WHERE d.id IN (" + ids + "))t;")
        if len(rows) >= len(manifest['documents']) and all(
                x['status'] == 'ready' and x['index_status'] == 'ready' for x in rows):
            return rows
        time.sleep(2)
    raise RuntimeError('Demo indexing did not finish; run preflight for diagnosis')


def setup():
    OUT.mkdir(parents=True, exist_ok=True)
    admin = admin_client()
    state = load(MANIFEST) if MANIFEST.exists() else {'documents': {}, 'templates': {}, 'drafts': {}}
    credentials = load(SECRETS) if SECRETS.exists() else {
        key: {'username': name, 'password': secrets.token_urlsafe(24)}
        for key, name in [('demo', 'demo-jury-20261024'), ('foreign', 'demo-outsider-20261024')]}
    # Persist before provisioning; a rerun never invents a new password.
    if not SECRETS.exists():
        save(SECRETS, credentials)
    for key, entry in credentials.items():
        if key not in state:
            users = list_all(admin, '/api/v1/users')
            found = [u for u in users if u['username'] == entry['username']]
            if found:
                # Verify ownership through saved credentials; never reset a password.
                check = session(entry['username'], entry['password'])
                user = request(check, 'GET', '/api/v1/auth/me').json()
                request(check, 'POST', '/api/v1/auth/logout', 204)
            else:
                user = request(admin, 'POST', '/api/v1/users', 201, json=entry).json()
            state[key] = user
            save(MANIFEST, state)
    sources = {
        'archive': ('DEMO Регламент учебного архива', 'archive.txt',
                    'УЧЕБНЫЕ СИНТЕТИЧЕСКИЕ ДАННЫЕ.\n1. Заявку на доступ к учебному архиву согласует куратор архива Тестова Анна.\n'
                    '2. Срок согласования заявки на доступ к учебному архиву — два рабочих дня.\n'
                    '3. После согласования доступ к учебному архиву выдаёт дежурный администратор.'),
        'rooms': ('DEMO Регламент учебных переговорных', 'rooms.txt',
                  'УЧЕБНЫЕ СИНТЕТИЧЕСКИЕ ДАННЫЕ.\n1. Бронирование учебной переговорной согласует координатор Тестов Борис.\n'
                  '2. Срок согласования бронирования учебной переговорной — четыре рабочих часа.'),
        'equipment': ('DEMO Регламент учебного оборудования', 'equipment.txt',
                      'УЧЕБНЫЕ СИНТЕТИЧЕСКИЕ ДАННЫЕ.\n1. Выдачу учебного проектора согласует техник Тестова Вера.\n'
                      '2. Учебный проектор возвращают технику до 18:30 того же дня.')}
    material_patterns = {'instruction': 'Инструкция*.pdf', 'matrix': 'Матрица*.docx',
                         'methods': 'Методические*.docx'}
    for key, pattern in material_patterns.items():
        path = next((ROOT/'data/demo').glob(pattern))
        sources[key] = ('DEMO Учебный материал ' + key, path.name, path.read_bytes())
    for key, (title, filename, content) in sources.items():
        content = content.encode('utf-8') if isinstance(content, str) else content
        if key in ('archive','rooms','equipment'):
            folder = OUT/'synthetic'
            folder.mkdir(exist_ok=True)
            local = folder/filename
            if not local.exists():
                local.write_bytes(content)
        sha = hashlib.sha256(content).hexdigest()
        if key not in state['documents']:
            # Recover a successful upload if setup was interrupted before manifest save.
            docs = list_all(admin, '/api/v1/documents')
            found = [d for d in docs if d['title'] == title]
            doc = None
            for candidate in found:
                details = request(admin, 'GET', '/api/v1/documents/' + candidate['id']).json()
                v = details['latest_version']
                original = request(admin, 'GET', f"/api/v1/documents/{candidate['id']}/versions/{v['id']}/download").content
                if hashlib.sha256(original).hexdigest() == sha:
                    doc = details
                    break
            if doc is None:
                doc = request(admin, 'POST', '/api/v1/documents', 201, data={'title': title},
                              files={'file': (filename, content)}).json()
            state['documents'][key] = {'id': doc['id'], 'version_id': doc['latest_version']['id'],
                                       'sha256': sha, 'title': title}
            save(MANIFEST, state)
        elif state['documents'][key]['sha256'] != sha:
            raise RuntimeError('Source changed; refusing to overwrite a demo document')
    catalog = list_all(admin, '/api/v1/templates')
    for key, profile, _ in cases():
        if key in state['templates']:
            t = request(admin, 'GET', '/api/v1/templates/'+state['templates'][key]['id']).json()
        else:
            candidates = [entry for entry in catalog if entry['schema_version']==2 and entry['enabled']
                          and entry['name'].startswith(('E2E v2 ', 'DEMO '))]
            t = None
            for candidate in candidates:
                detail = request(admin,'GET','/api/v1/templates/'+candidate['id']).json()
                if detail['profile']==profile:
                    t = detail
                    break
            if t is None:
                path = source_path(profile)
                original = path.read_bytes()
                assert hashlib.sha256(original).hexdigest()==profile['source_sha256']
                title = 'DEMO Модуль '+key
                existing = [d for d in list_all(admin,'/api/v1/documents') if d['title']==title]
                doc = None
                for candidate in existing:
                    detail = request(admin,'GET','/api/v1/documents/'+candidate['id']).json()
                    version_id = detail['latest_version']['id']
                    content = request(admin,'GET',f"/api/v1/documents/{candidate['id']}/versions/{version_id}/download").content
                    if content==original:
                        doc = detail
                        break
                if doc is None:
                    doc = request(admin,'POST','/api/v1/documents',201,data={'title':title},
                                  files={'file':(path.name,original)}).json()
                t = request(admin,'POST','/api/v1/templates',201,json={
                    'document_id':doc['id'],'document_version_id':doc['latest_version']['id'],
                    'name':title,'profile':profile}).json()
        assert t['name'].startswith(('E2E v2 ', 'DEMO ')) and t['schema_version'] == 2 and t['enabled']
        assert t['profile']==profile
        state['templates'][key] = {'id': t['id'], 'document_id': t['document_id'],
                                    'version_id': t['document_version_id'], 'name': t['name']}
        state['documents'][key] = {'id': t['document_id'], 'version_id': t['document_version_id'],
                                   'sha256': t['profile']['source_sha256'], 'title': t['name']}
    save(MANIFEST, state)
    for doc in state['documents'].values():
        path = '/api/v1/documents/' + doc['id'] + '/access'
        users = request(admin, 'GET', path + '?limit=100&offset=0').json()['items']
        if state['demo']['id'] not in {u['id'] for u in users}:
            request(admin, 'POST', path, 201, json={'user_id': state['demo']['id']})
    wait_indexes(state)
    cred = credentials['demo']
    demo = session(cred['username'],cred['password'])
    values = {key: v for key, _, v in cases()}
    for key in ('working-group', 'is-commissioning'):
        if key not in state['drafts']:
            draft = request(demo, 'POST', '/api/v1/templates/' + state['templates'][key]['id'] + '/drafts', 201).json()
            state['drafts'][key] = draft['id']
            save(MANIFEST, state)
        else:
            draft = request(demo,'GET','/api/v1/template-drafts/'+state['drafts'][key]).json()
        if draft['revision']==1 and not draft['values']:
            # Recover an interrupted preparation, without overwriting edited drafts.
            draft = request(demo, 'PATCH', '/api/v1/template-drafts/' + draft['id'],
                            json={'expected_revision': draft['revision'], 'values': values[key]}).json()
    save(OUT/'example-values.json', values)
    for c in (admin, demo):
        request(c, 'POST', '/api/v1/auth/logout', 204)
    print('PASS setup: demo-jury-20261024; 13 documents, seven v2 modules, two reserve drafts. Credentials saved locally; not printed.')


def rehearsal():
    state = load(MANIFEST)
    demo, foreign = clients()
    admin = admin_client()
    report = {'questions': [], 'security': {}, 'generation': []}
    sampler_stop = threading.Event()
    samples = []
    def sample():
        while not sampler_stop.is_set():
            p = subprocess.run(['docker','stats','--no-stream','--format','{{json .}}',
                                'hackathon-api-1','hackathon-ai-service-1',
                                'hackathon-postgres-1','hackathon-frontend-1'],
                               capture_output=True, text=True, timeout=15)
            samples.extend(json.loads(line) for line in p.stdout.splitlines() if line.startswith('{'))
            save(OUT/'resources.json', samples)
            sampler_stop.wait(2)
    threading.Thread(target=sample, daemon=True).start()
    questions = load(ROOT/'scripts/demo-questions.json')
    chat_id = None
    for q in questions:
        start = time.perf_counter()
        search = request(demo, 'POST', '/api/v1/search', json={'query': q['query'], 'limit': 10}).json()
        search_ms = round((time.perf_counter()-start)*1000)
        if not q.get('follow_up') or not chat_id:
            chat_id = request(demo, 'POST', '/api/v1/chat/sessions', 201,
                              json={'title': 'DEMO репетиция ' + q['id']}).json()['id']
        start = time.perf_counter()
        turn = request(demo, 'POST', f'/api/v1/chat/sessions/{chat_id}/messages',
                       json={'content': q['query'], 'client_message_id': str(uuid4())}).json()
        chat_ms = round((time.perf_counter()-start)*1000)
        allowed = {d['id'] for d in state['documents'].values()}
        assert {s['document_id'] for s in turn['sources']} <= allowed
        entry = {**q, 'search_ms': search_ms, 'chat_ms': chat_ms, 'search': search,
                 'turn': turn, 'session_id': chat_id}
        report['questions'].append(entry)
        save(OUT/'rehearsal.json', report)
        print(q['id'], 'mode='+turn['mode'], 'search_ms='+str(search_ms), 'chat_ms='+str(chat_ms), flush=True)
    for key, profile, values in cases():
        t = state['templates'][key]
        draft = request(demo, 'POST', '/api/v1/templates/' + t['id'] + '/drafts', 201).json()
        path = '/api/v1/template-drafts/' + draft['id']
        request(foreign, 'GET', path, 404)
        request(admin, 'GET', path, 404)
        request(demo, 'PATCH', path, 403, csrf=False,
                json={'expected_revision': draft['revision'], 'values': values})
        request(demo, 'POST', path + '/generate', 422, json={'expected_revision': draft['revision']})
        draft = request(demo, 'PATCH', path, json={'expected_revision': draft['revision'], 'values': values}).json()
        assert draft['validation']['ready']
        start = time.perf_counter()
        g = request(demo, 'POST', path+'/generate', json={'expected_revision': draft['revision']}).json()
        ms = round((time.perf_counter()-start)*1000)
        r = request(demo, 'GET', g['download_url'])
        verify(profile, values, r.content)
        assert r.headers['content-type'].split(';')[0].endswith('wordprocessingml.document')
        request(foreign, 'GET', g['download_url'], 404)
        request(foreign, 'GET', '/api/v1/generated-documents/'+g['generated_document_id'], 404)
        (OUT/(key+'.docx')).write_bytes(r.content)
        original = request(demo, 'GET', f"/api/v1/documents/{t['document_id']}/versions/{t['version_id']}/download").content
        assert hashlib.sha256(original).hexdigest() == profile['source_sha256']
        report['generation'].append({'module': key, 'draft_id': draft['id'], **g,
                                     'ms': ms, 'sha256': hashlib.sha256(r.content).hexdigest(),
                                     'manual_review': sum(c['category']=='manual_review' for c in draft['validation']['checks'])})
        save(OUT/'rehearsal.json', report)
        print('PASS generation', key, ms, 'ms', flush=True)
    doc = state['documents']['archive']
    path = '/api/v1/documents/' + doc['id']
    request(foreign, 'GET', path, 404)
    request(foreign, 'GET', path+'/versions/'+doc['version_id']+'/download', 404)
    request(demo, 'POST', '/api/v1/search', 403, csrf=False, json={'query':'архив','limit':10})
    entry = report['generation'][0]
    t = state['templates'][entry['module']]
    access = f"/api/v1/documents/{t['document_id']}/access"
    request(admin, 'DELETE', access+'/'+state['demo']['id'], 204)
    try:
        request(demo, 'GET', '/api/v1/template-drafts/'+entry['draft_id'], 404)
        request(demo, 'GET', entry['download_url'], 404)
    finally:
        request(admin, 'POST', access, 201, json={'user_id':state['demo']['id']})
    request(admin, 'DELETE', path+'/access/'+state['demo']['id'], 204)
    try:
        request(demo, 'GET', path, 404)
        results = request(demo, 'POST', '/api/v1/search', json={'query':questions[0]['query'],'limit':10}).json()['results']
        assert doc['id'] not in {s['document_id'] for s in results}
        history = request(demo, 'GET', '/api/v1/chat/sessions/'+report['questions'][0]['session_id']).json()
        assert any('изменения прав доступа' in m['content'] for m in history['messages'])
    finally:
        request(admin, 'POST', path+'/access', 201, json={'user_id':state['demo']['id']})
    report['security'] = {k:'PASS' for k in ['document_acl','foreign_draft','foreign_generated_uuid',
                        'admin_not_draft_owner','csrf','revoke_download','revoke_search','revoke_history']}
    report['browser_docx'] = []
    for browser_path in (OUT/'browser-verification.json', OUT/'browser-working-group.json'):
        if not browser_path.exists():
            continue
        browser = load(browser_path)
        result_id = str(UUID(browser['resultUrl'].rsplit('/',1)[1]))
        metadata = request(demo,'GET','/api/v1/generated-documents/'+result_id).json()
        snapshot = metadata['generation_snapshot']
        actual = Path(browser['download']).read_bytes()
        assert hashlib.sha256(actual).hexdigest()==metadata['sha256']
        assert actual==request(demo,'GET','/api/v1/generated-documents/'+result_id+'/download').content
        verify(snapshot['profile'],snapshot['values'],actual)
        browser.update(sha256=metadata['sha256'],revision=metadata['draft_revision'],
                       structural_verification='PASS',bytes_match_api=True)
        save(browser_path,browser)
        report['browser_docx'].append(browser)
    save(OUT/'rehearsal.json', report)
    sampler_stop.set()
    for c in (admin, demo, foreign):
        request(c, 'POST', '/api/v1/auth/logout', 204)
    print('PASS public API generation and security; review recorded answers separately.')


def resilience():
    """Explicit rehearsal only: briefly stops Python and restarts existing services.

    Preserves all named volumes. Never used by preflight.
    """
    state = load(MANIFEST)
    cred = load(SECRETS)['demo']
    demo = session(cred['username'], cred['password'])
    previous = load(OUT/'rehearsal.json')['generation'][0]
    content = request(demo, 'GET', previous['download_url']).content
    template = state['templates']['working-group']
    draft = request(demo, 'POST', '/api/v1/templates/'+template['id']+'/drafts', 201).json()
    path = '/api/v1/template-drafts/'+draft['id']
    values = load(OUT/'example-values.json')['working-group']
    draft = request(demo, 'PATCH', path, json={'expected_revision':draft['revision'],'values':values}).json()
    chat = request(demo, 'POST', '/api/v1/chat/sessions', 201, json={'title':'DEMO Python outage'}).json()['id']
    report = {}
    subprocess.run(['docker','compose','stop','ai-service'], cwd=ROOT, capture_output=True, check=True, timeout=60)
    try:
        start = time.perf_counter()
        results = request(demo, 'POST', '/api/v1/search', json={'query':'заявку на доступ к учебному архиву','limit':10}).json()
        assert state['documents']['archive']['id'] in {s['document_id'] for s in results['results']}
        turn = request(demo, 'POST', f'/api/v1/chat/sessions/{chat}/messages',
                       json={'content':'Кто согласует заявку на доступ к учебному архиву?','client_message_id':str(uuid4())}).json()
        assert turn['mode']=='search' and turn['sources']
        assert request(demo, 'GET', previous['download_url']).content == content
        failure = request(demo, 'POST', path+'/generate', 502, json={'expected_revision':draft['revision']})
        report['python_outage'] = {'search':'PASS FTS fallback','chat_mode':turn['mode'],
                                    'download':'PASS identical bytes','generate_status':failure.status_code,
                                    'total_ms':round((time.perf_counter()-start)*1000)}
    finally:
        subprocess.run(['docker','compose','start','ai-service'],cwd=ROOT,capture_output=True,check=True,timeout=60)
    def ready():
        start = time.monotonic()
        while time.monotonic()-start < 150:
            p = subprocess.run(['docker','compose','ps','--format','json'],cwd=ROOT,capture_output=True,text=True)
            rows = [json.loads(line) for line in p.stdout.splitlines() if line.startswith('{')]
            if len(rows)==4 and all(r['Health']=='healthy' for r in rows):
                probe = subprocess.run(['docker','compose','exec','-T','ai-service','python','-','embedding'],
                                       input=(ROOT/'scripts/demo-ai-probe.py').read_text(encoding='utf-8'),
                                       cwd=ROOT,text=True,encoding='utf-8',capture_output=True,timeout=50)
                if probe.returncode==0:
                    return
            time.sleep(2)
        raise RuntimeError('Recovery readiness deadline exceeded')
    ready()
    g = request(demo, 'POST', path+'/generate', json={'expected_revision':draft['revision']}).json()
    verify(draft['profile'],values,request(demo, 'GET', g['download_url']).content)
    report['python_recovery'] = 'PASS retry generated a valid DOCX'
    tables = ['documents','document_versions','document_access','document_chunks',
              'semantic_indexes','semantic_chunks','embedding_windows','document_templates',
              'template_drafts','template_generated_files','chat_sessions','chat_messages','chat_message_sources']
    def fingerprint():
        return {table:sql("SELECT json_build_object('rows',count(*),'md5',md5(string_agg(data,E'\\n' ORDER BY data))) "
                          "FROM (SELECT row_to_json(t)::text data FROM "+table+" t)s;") for table in tables}
    before = fingerprint()
    start = time.perf_counter()
    subprocess.run(['docker','compose','restart','postgres','ai-service','api','frontend'],
                   cwd=ROOT,capture_output=True,check=True,timeout=120)
    ready()
    report['restart_readiness_ms'] = round((time.perf_counter()-start)*1000)
    after = fingerprint()
    assert before==after, 'Persistent data changed across restart'
    assert request(demo, 'GET', previous['download_url']).content==content
    assert request(demo, 'GET', path).json()['values']==draft['values']
    assert request(demo, 'GET', f'/api/v1/chat/sessions/{chat}').json()['messages']
    report['persistence'] = {'status':'PASS','tables':before,'session_survives':True,
                             'generated_sha256':hashlib.sha256(content).hexdigest()}
    save(OUT/'resilience.json',report)
    request(demo,'POST','/api/v1/auth/logout',204)
    print('PASS Python outage/recovery, restart readiness and persistent fingerprints for 13 tables.')


def questions_only():
    """Repeat fixed control questions; keep the original failing run as evidence."""
    cred = load(SECRETS)['demo']
    demo = session(cred['username'],cred['password'])
    state = load(MANIFEST)
    rows = []
    chat = None
    questions = load(ROOT/'scripts/demo-questions.json')
    questions += [dict(next(q for q in questions if q['id']=='module-is-date'),
                       id='module-is-date-repeat-'+str(i)) for i in range(1,4)]
    for q in questions:
        start = time.perf_counter()
        found = request(demo,'POST','/api/v1/search',json={'query':q['query'],'limit':10}).json()
        search_ms = round((time.perf_counter()-start)*1000)
        if not q.get('follow_up') or chat is None:
            chat = request(demo,'POST','/api/v1/chat/sessions',201,json={'title':'DEMO final '+q['id']}).json()['id']
        start = time.perf_counter()
        turn = request(demo,'POST',f'/api/v1/chat/sessions/{chat}/messages',
                       json={'content':q['query'],'client_message_id':str(uuid4())}).json()
        citations = turn['sources']
        allowed = {d['id'] for d in state['documents'].values()}
        assert {s['document_id'] for s in citations} <= allowed
        expected_id = state['documents'][q['source']]['id'] if q['source'] else None
        rows.append({**q,'turn':turn,'search':found,'session_id':chat,
                     'search_ms':search_ms,'chat_ms':round((time.perf_counter()-start)*1000),
                     'expected_source_cited':expected_id in {s['document_id'] for s in citations} if expected_id else not citations})
        save(OUT/'post-fix-questions.json',rows)
        print(q['id'],turn['mode'],'expected_source='+str(rows[-1]['expected_source_cited']),flush=True)
    request(demo,'POST','/api/v1/auth/logout',204)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['setup', 'rehearse', 'resilience', 'questions'])
    args = parser.parse_args()
    try:
        {'setup':setup,'rehearse':rehearsal,'resilience':resilience,'questions':questions_only}[args.action]()
    except Exception as error:
        # Do not expose HTTP bodies or credentials on a failed login.
        print('FAIL demo ' + args.action + ': ' + type(error).__name__ + '. Inspect local state and run preflight.')
        raise SystemExit(1)
