"""Verify the browser-created local demo member, without changing users or ACL.

Uses cookie sessions and public HTTP APIs. Only login/logout and deliberately
denied writes change session state. Never prints credentials or response bodies.
"""
import argparse
from http.cookiejar import CookieJar
import json
from pathlib import Path
import sys
from urllib.error import HTTPError
from urllib.parse import urlparse
from urllib.request import HTTPCookieProcessor, Request, build_opener

ROOT = Path(__file__).resolve().parents[1]


class Client:
    def __init__(self, base):
        self.base = base
        self.cookies = CookieJar()
        self.opener = build_opener(HTTPCookieProcessor(self.cookies))

    def request(self, path, method='GET', body=None, csrf=True, origin=None):
        headers = {'Accept': 'application/json'}
        data = None
        if method != 'GET':
            headers['Origin'] = origin or self.base
            if csrf:
                for cookie in self.cookies:
                    if cookie.name in ('ai_csrf', '__Host-ai_csrf'):
                        headers['X-CSRF-Token'] = cookie.value
        if body is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(body).encode()
        req = Request(self.base + '/api/v1' + path, data=data, headers=headers, method=method)
        try:
            response = self.opener.open(req, timeout=20)
        except HTTPError as error:
            response = error
        with response:
            raw = response.read()
            value = json.loads(raw) if raw and 'application/json' in response.headers.get('Content-Type', '') else None
            return response.status, value

    def login(self, credentials):
        status, result = self.request('/auth/login', 'POST', credentials)
        assert status == 200, 'login failed'
        return result['user']

    def all(self, path):
        result = []
        while True:
            status, page = self.request(f'{path}?limit=100&offset={len(result)}')
            assert status == 200, 'list failed'
            result.extend(page['items'])
            if len(result) >= page['total']:
                return result
            assert page['items'], 'incomplete list'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://localhost:8080')
    parser.add_argument('--credentials', type=Path, default=ROOT / '.local-integration/users-page/credentials.json')
    parser.add_argument('--allowed-title', default='DEMO Регламент учебного оборудования')
    args = parser.parse_args()
    base = args.base_url.rstrip('/')
    assert urlparse(base).hostname in ('localhost', '127.0.0.1', '::1'), 'local URLs only'
    env = {}
    for line in (ROOT / '.env').read_text(encoding='utf-8-sig').splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            env[key.strip()] = value.strip().strip('\"\'')
    credentials = json.loads(args.credentials.read_text(encoding='utf-8-sig'))
    admin, member = Client(base), Client(base)
    checks = []

    def check(name, passed):
        assert passed, name
        checks.append(name)
        print('PASS ' + name)

    try:
        check('admin login', admin.login({'username': env.get('ADMIN_USERNAME', 'admin'), 'password': env['ADMIN_PASSWORD']})['role'] == 'admin')
        identity = member.login(credentials)
        check('new account login as member', identity['role'] == 'member')
        users = admin.all('/users')
        user = next(user for user in users if user['id'] == identity['id'])
        check('real public fields and active member', set(user) == {'id', 'username', 'role', 'is_active'} and user['is_active'] and user['role'] == 'member')
        documents = admin.all('/documents')
        visible = member.all('/documents')
        check('only the selected document is visible', len(visible) == 1 and visible[0]['title'] == args.allowed_title)
        allowed = visible[0]['id']
        check('allowed metadata and download', member.request(f'/documents/{allowed}')[0] == 200 and member.request(f'/documents/{allowed}/download')[0] == 200)
        denied = [doc for doc in documents if doc['id'] != allowed]
        check('every other document denies direct metadata access', bool(denied) and all(member.request('/documents/' + doc['id'])[0] == 404 for doc in denied))
        revoked = next(doc['id'] for doc in denied if doc['title'] == 'DEMO Регламент учебного архива')
        check('revoked document denies download and version history', member.request(f'/documents/{revoked}/download')[0] == 404 and member.request(f'/documents/{revoked}/versions')[0] == 404)
        check('member cannot list users or ACL', member.request('/users')[0] == 403 and member.request(f'/documents/{allowed}/access')[0] == 403)
        payload = {'username': credentials['username'], 'password': credentials['password']}
        check('member cannot create accounts or supply admin role', member.request('/users', 'POST', payload)[0] == 403 and member.request('/users', 'POST', {**payload, 'role': 'admin'})[0] == 403)
        check('member cannot grant or revoke ACL', member.request(f'/documents/{allowed}/access', 'POST', {'user_id': identity['id']})[0] == 403 and member.request(f'/documents/{allowed}/access/{identity["id"]}', 'DELETE')[0] == 403)
        check('admin creation requires CSRF', admin.request('/users', 'POST', payload, csrf=False)[0] == 403)
        check('ACL writes require CSRF', admin.request(f'/documents/{allowed}/access', 'POST', {'user_id': identity['id']}, csrf=False)[0] == 403 and admin.request(f'/documents/{allowed}/access/{identity["id"]}', 'DELETE', csrf=False)[0] == 403)
        check('untrusted Origin is rejected', admin.request('/users', 'POST', payload, origin='http://untrusted.invalid')[0] == 403)
        check('duplicate login conflicts', admin.request('/users', 'POST', payload)[0] == 409)
        check('admin cannot inject role into creation', admin.request('/users', 'POST', {**payload, 'role': 'admin'})[0] == 400)
        status, history = member.request('/chat/sessions')
        check('demo chat history is clean', status == 200 and history['sessions'] == [])
        report = {'username': identity['username'], 'allowed_document_id': allowed, 'denied_documents': len(denied), 'checks': checks}
        (args.credentials.parent / 'verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    finally:
        for client in (member, admin):
            client.request('/auth/logout', 'POST')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Exception/request representations may include credentials. Keep failures safe.
        print('FAIL local users/ACL verification; inspect the last PASS and local configuration.', file=sys.stderr)
        sys.exit(1)
