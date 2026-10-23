# Authentication API

The backend uses Argon2id, PostgreSQL sessions, an HttpOnly session cookie and a
separate readable CSRF cookie. There is no JWT, public registration, or token
storage in localStorage/sessionStorage.

## Start locally

Set process environment variables (the application does not load `.env`):

```powershell
$env:DATABASE_URL = 'postgres://app:password@localhost:5432/ai_assistant?sslmode=disable'
$env:APP_ENV = 'development'
$env:HTTP_PORT = '8080'
$env:BOOTSTRAP_ADMIN = 'true'
$env:ADMIN_USERNAME = 'admin'
$env:ADMIN_PASSWORD = '<unique password of at least 12 characters>'
$env:COOKIE_SECURE = 'false'
$env:ALLOWED_ORIGINS = 'http://localhost:5173,http://localhost:8080'
go run ./cmd/server
```

Startup connects to PostgreSQL, migrates/verifies the schema, bootstraps the first
administrator, and runs HTTP. A bootstrap failure stops startup. If ANY user
already exists, bootstrap never creates/promotes/updates an account and does
not need ADMIN_PASSWORD. You can remove that secret from the environment after
initial provisioning. BOOTSTRAP_ADMIN=false disables provisioning entirely.

The initial insert rechecks emptiness under a PostgreSQL SHARE ROW EXCLUSIVE
table lock inside a read-committed transaction. This serializes concurrent
instances and ordinary inserts. Existing rows are never overwritten.

SIGINT/SIGTERM starts HTTP shutdown (10 seconds to drain), then closes the pool
and flushes the logger. Migration CLI commands return before bootstrap and HTTP:

```powershell
go run ./cmd/server migrate status
go run ./cmd/server migrate version
```

## HTTP contract

All application responses set Cache-Control: no-store. Public user objects
contain only id, username and role. Errors have the form `{"error":"message"}`.
The service never sends stored hashes or raw tokens in JSON.

| Method and path | Input | Success | Errors |
| --- | --- | --- | --- |
| POST /api/v1/auth/login | application/json: username, password; trusted Origin | 200: `{"user":{"id":"uuid","username":"admin","role":"admin"}}`; two Set-Cookie headers | 400 invalid JSON/input; 401 invalid credentials; 403 Origin denied; 429 rate limit; 500 storage/internal error |
| GET /api/v1/auth/me | Session cookie | 200: `{"id":"uuid","username":"admin","role":"admin"}` | 401 invalid/expired/revoked session or inactive user; 500 storage error |
| POST /api/v1/auth/logout | Session cookie, trusted Origin, X-CSRF-Token for an active session | 204; revoke session and expire both cookies | 403 invalid/missing CSRF or Origin; 500 storage error |
| GET /api/v1/admin/ping | Active admin session | 200: `{"status":"ok"}` | 401 unauthenticated; 403 wrong role; 500 storage error |

The admin ping route is **diagnostic** and can be removed when real admin routes
are added. Unknown usernames, wrong passwords and inactive users all receive
the same 401 JSON. Unknown usernames still execute an Argon2id verification.
Login JSON is capped at 4096 bytes and rejects unknown fields and trailing JSON.

Logout with an absent/already invalid session still requires a trusted Origin,
returns 204 and clears cookies. Logout with an active session always requires
CSRF. Repeated revocation preserves the first revoked_at timestamp.

GET/HEAD do not require CSRF or Origin. Every protected state-changing request
must use the CSRF middleware. Origin matching is exact: scheme, hostname, port;
missing, null, wildcard, duplicate and untrusted origins are rejected for writes.

Example requests (replace the password and CSRF placeholder locally):

```bash
curl -i -c cookies.txt http://localhost:8080/api/v1/auth/login \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"<your password>"}'

curl -i -b cookies.txt http://localhost:8080/api/v1/auth/me

curl -i -b cookies.txt -c cookies.txt -X POST http://localhost:8080/api/v1/auth/logout \
  -H 'Origin: http://localhost:8080' \
  -H 'X-CSRF-Token: <value of ai_csrf cookie>'
```

Cookie files contain credentials: use them only locally and do not commit them.
Browser fetch calls should use `credentials: "include"`. Read the readable CSRF
cookie and send its value as X-CSRF-Token on writes. CORS preflights allow only
exact trusted origins, credentials, GET/POST/DELETE, Content-Type and X-CSRF-Token;
there is no wildcard CORS.

## Passwords, sessions and limits

New passwords require 12 Unicode characters and at most 1024 UTF-8 bytes.
Argon2id uses 19 MiB, 2 iterations, 1 lane, a random 16-byte salt and a 32-byte
key. PHC strings carry parameters; verification uses constant-time comparison
and rejects malformed or excessive parameters before allocating. At most two
login password verifications run simultaneously per AuthService.

Both session and CSRF tokens are independent crypto/rand 32-byte values, encoded
as canonical unpadded base64url. Only SHA-256 hashes of the decoded bytes are
stored. SESSION_TTL defaults to 12h; expiry is absolute, with no sliding renewal.
Authentication rechecks expiry, revocation and user activity in PostgreSQL on
each request. Role changes/deactivation take effect on subsequent requests.

The in-memory fixed-window login limiter allows 10 attempts per direct peer IP
and 100 globally per 15 minutes, including successful and malformed attempts.
It emits 429 with Retry-After. It does not key on usernames and ignores
X-Forwarded-For. State is bounded, per process, and resets on restart; multiple
replicas do not share limits. A reverse proxy would currently share one peer IP.
Distributed throttling and trusted-proxy configuration are future deployment work.

## Production HTTPS

Production fails startup unless COOKIE_SECURE=true, explicit HTTPS origins and
both HTTP_TLS_CERT_FILE / HTTP_TLS_KEY_FILE are configured. TLS is terminated
by this Go server (TLS 1.2 minimum), with no trust in X-Forwarded-Proto. A proxy
must connect to the backend over TLS; plaintext TLS-termination-only deployments
are not supported by this version. Migration CLI does not require TLS settings.

```dotenv
APP_ENV=production
HTTP_PORT=8443
COOKIE_SECURE=true
ALLOWED_ORIGINS=https://assistant.example.com:8443
HTTP_TLS_CERT_FILE=/run/secrets/tls.crt
HTTP_TLS_KEY_FILE=/run/secrets/tls.key
SESSION_TTL=12h
```

Production cookie names are __Host-ai_session (HttpOnly) and __Host-ai_csrf
(readable). Both use Secure, SameSite=Lax, Path=/, no Domain, and MaxAge/Expires
matching server expiry. Plain HTTP development uses ai_session and ai_csrf with
COOKIE_SECURE=false. Secure development cookies also use the __Host- names.

Serve frontend and API on the same hostname so JavaScript can read the CSRF
cookie; different localhost ports work for development. A frontend on a different
hostname cannot read the API host's __Host- CSRF cookie. Password changes/session
renewal, account/role editing, MFA, and a session-cleanup scheduler are not included.
Admin-only member creation and document ACL are described in [documents.md](documents.md).

## Tests

```powershell
go test ./...
go vet ./...
go build ./cmd/server
$env:TEST_DATABASE_URL = 'postgres://test:password@localhost:5432/auth_test?sslmode=disable'
go test ./... -count=1
```

Unit tests require no PostgreSQL. Integration tests only use explicit
TEST_DATABASE_URL, require a database name ending in _test, and create/drop only
their own random schemas. They cover real HTTP and HTTPS login/me/logout,
cookie/CSRF/RBAC behavior, PostgreSQL sessions and concurrent bootstrap. Never
select an existing user or production database for these tests.
