"""Companion fixtures and public API checks for the React browser walkthrough.

Browser interactions use the browser tool, not this script. Only synthetic users
and existing labelled E2E documents are used. Credentials never enter reports.
Set TEMPLATE_WIZARD_TEST_PASSWORD in the calling process. No production data is deleted.
"""
import argparse
import hashlib
import io
import json
import os
import secrets
from pathlib import Path
from zipfile import ZipFile
from lxml import etree
from dotenv import dotenv_values
from template_e2e import ROOT, request, session, compose, health
from template_v2_e2e import verify
from template_v2_cases import cases

OUT = ROOT / '.local-integration/template-wizard'
FIXTURE = OUT / 'fixtures.json'


def setup():
    OUT.mkdir(parents=True, exist_ok=True)
    env = dotenv_values(ROOT / '.env')
    admin = session(env.get('ADMIN_USERNAME', 'admin'), env['ADMIN_PASSWORD'])
    suffix = secrets.token_hex(4)
    users = []
    for index in range(2):
        username = f'wizard_ui_{suffix}_{index}'
        user = request(admin, 'POST', '/api/v1/users', 201, json=dict(username=username, password=os.environ['TEMPLATE_WIZARD_TEST_PASSWORD'])).json()
        users.append(dict(username=username, id=user['id']))
    seeds = json.loads((ROOT / '.local-integration/template-v2-e2e/verification.json').read_text(encoding='utf-8'))
    templates = {}
    for seed in seeds:
        t = request(admin, 'GET', '/api/v1/templates/' + seed['template_id']).json()
        assert t['name'].startswith('E2E v2 '), 'Only labelled synthetic sources are allowed'
        request(admin, 'POST', f"/api/v1/documents/{t['document_id']}/access", 201, json={'user_id': users[0]['id']})
        templates[seed['module']] = t
    data = dict(users=users, templates=templates, values={key: values for key, _, values in cases()})
    FIXTURE.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(dict(username=users[0]['username'], modules=list(templates)), ensure_ascii=False))


def clients(roles=('admin', 'user', 'foreign')):
    f = json.loads(FIXTURE.read_text(encoding='utf-8'))
    env = dotenv_values(ROOT / '.env')
    admin = session(env.get('ADMIN_USERNAME', 'admin'), env['ADMIN_PASSWORD']) if 'admin' in roles else None
    user = session(f['users'][0]['username'], os.environ['TEMPLATE_WIZARD_TEST_PASSWORD']) if 'user' in roles else None
    foreign = session(f['users'][1]['username'], os.environ['TEMPLATE_WIZARD_TEST_PASSWORD']) if 'foreign' in roles else None
    return f, admin, user, foreign


def security_restart():
    f, admin, user, foreign = clients()
    drafts = request(user, 'GET', '/api/v1/template-drafts?limit=100&offset=0').json()['items']
    assert drafts
    verified = []
    for draft in drafts:
        path = '/api/v1/template-drafts/' + draft['id']
        request(foreign, 'GET', path, 404)
        request(admin, 'GET', path, 404)
        request(user, 'PATCH', path, 403, csrf=False, json={'expected_revision': draft['revision'], 'values': {}})
        if not draft['validation']['ready']:
            continue
        g = request(user, 'POST', path + '/generate', json={'expected_revision': draft['revision']}).json()
        again = request(user, 'POST', path + '/generate', json={'expected_revision': draft['revision']}).json()
        assert g['generated_document_id'] == again['generated_document_id']
        request(foreign, 'GET', '/api/v1/generated-documents/' + g['generated_document_id'], 404)
        content = request(user, 'GET', g['download_url']).content
        verify(draft['profile'], draft['values'], content)
        output = OUT / (draft['profile']['profile_id'] + '-' + str(draft['revision']) + '.docx')
        output.write_bytes(content)
        template = f['templates'][draft['profile']['profile_id']]
        access = '/api/v1/documents/' + template['document_id'] + '/access'
        request(admin, 'DELETE', access + '/' + f['users'][0]['id'], 204)
        request(user, 'GET', path, 404)
        request(user, 'GET', g['download_url'], 404)
        assert draft['id'] not in {d['id'] for d in request(user, 'GET', '/api/v1/template-drafts?limit=100&offset=0').json()['items'] or []}
        request(admin, 'POST', access, 201, json={'user_id': f['users'][0]['id']})
        assert request(user, 'GET', g['download_url']).content == content
        verified.append(dict(draft_id=draft['id'], module=draft['profile']['profile_id'], result=g['generated_document_id'], revision=draft['revision'], sha256=hashlib.sha256(content).hexdigest(), file=str(output)))
    # Resume a deliberately partial draft after restarting stateless services.
    t = f['templates']['working-group']
    partial = request(user, 'POST', '/api/v1/templates/' + t['id'] + '/drafts', 201).json()
    partial = request(user, 'POST', '/api/v1/template-drafts/' + partial['id'] + '/answers', json={'expected_revision': partial['revision'], 'field_key': 'organization', 'value': 'Учебный черновик после restart'}).json()
    compose('restart', 'api', 'ai-service', 'frontend')
    health()
    f, admin, user, foreign = clients()
    resumed = request(user, 'GET', '/api/v1/template-drafts/' + partial['id']).json()
    assert resumed['revision'] == partial['revision'] and resumed['values'] == partial['values']
    # Finish that same partial draft after restart through the public question API.
    path = '/api/v1/template-drafts/' + resumed['id']
    while resumed['validation']['next_question']:
        field = resumed['validation']['next_question']['key']
        resumed = request(user, 'POST', path + '/answers', json={
            'expected_revision': resumed['revision'], 'field_key': field,
            'value': f['values']['working-group'][field]}).json()
    assert resumed['validation']['ready']
    generated = request(user, 'POST', path + '/generate', json={'expected_revision': resumed['revision']}).json()
    verify(resumed['profile'], resumed['values'], request(user, 'GET', generated['download_url']).content)
    for entry in verified:
        content = request(user, 'GET', '/api/v1/generated-documents/' + entry['result'] + '/download').content
        assert hashlib.sha256(content).hexdigest() == entry['sha256']
    (OUT / 'api-verification.json').write_text(json.dumps(dict(checks=verified, partial_draft=partial['id'], security='passed', restart='passed'), ensure_ascii=False, indent=2), encoding='utf-8')
    print(f'PASS: {len(verified)} generated revisions, ownership, CSRF, ACL revoke/restore, partial draft and downloads after stateless restart')


def verify_browser():
    """Verify files downloaded by actual browser UI against immutable server snapshots."""
    _, _, user, _ = clients(('user',))
    runs = json.loads((OUT / 'browser-verification.json').read_text(encoding='utf-8'))
    for entry in runs:
        result_id = entry['resultUrl'].rsplit('/', 1)[1]
        metadata = request(user, 'GET', '/api/v1/generated-documents/' + result_id).json()
        snapshot = metadata['generation_snapshot']
        content = Path(entry['download']).read_bytes()
        assert hashlib.sha256(content).hexdigest() == metadata['sha256']
        assert content == request(user, 'GET', '/api/v1/generated-documents/' + result_id + '/download').content
        verify(snapshot['profile'], snapshot['values'], content)
        if entry['module'] == 'project-committee':
            with ZipFile(io.BytesIO(content)) as archive:
                root = etree.fromstring(archive.read('word/document.xml'))
            ns = {'w': 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'}
            paragraphs = [''.join(p.itertext()) for p in root.xpath('//w:p', namespaces=ns)]
            control = [text for text in paragraphs if 'Контроль' in text]
            expected_number = '3.' if snapshot['values']['repeal'] == 'yes' else '2.'
            assert any(text.startswith(expected_number) for text in control), control
        entry['sha256'] = metadata['sha256']
        entry['revision'] = metadata['draft_revision']
        entry['structural_verification'] = 'passed'
        print('PASS: browser DOCX, snapshot, cleanup, protected zones:', entry.get('label', entry['module']))
    (OUT / 'browser-verification.json').write_text(json.dumps(runs, ensure_ascii=False, indent=2), encoding='utf-8')


def browser_acl(restore=False):
    f, admin, _, _ = clients(('admin',))
    template = f['templates']['working-group']
    assert template['name'].startswith('E2E v2 ')
    path = '/api/v1/documents/' + template['document_id'] + '/access'
    if restore:
        request(admin, 'POST', path, 201, json={'user_id': f['users'][0]['id']})
    else:
        request(admin, 'DELETE', path + '/' + f['users'][0]['id'], 204)
    print('Synthetic working-group ACL ' + ('restored' if restore else 'revoked'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['setup', 'security-restart', 'verify-browser', 'browser-revoke', 'browser-restore'])
    args = parser.parse_args()
    {'setup': setup, 'security-restart': security_restart, 'verify-browser': verify_browser,
     'browser-revoke': browser_acl, 'browser-restore': lambda: browser_acl(True)}[args.mode]()
