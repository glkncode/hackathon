# Запуск AI Document Assistant через Docker

## Требования и состояние репозитория

Нужны Git и Docker Desktop с Linux containers / WSL 2. Локальные Go, Python,
Node.js и PostgreSQL не требуются. В Docker Desktop включите WSL 2 engine,
дождитесь состояния Engine running; выполните `docker version` и
`docker compose version` в PowerShell. Если WSL 2 отсутствует, установите его
через `wsl --install` и перезагрузите Windows согласно инструкции Microsoft.
Для VPS достаточно Docker Engine с Compose plugin; Docker Desktop там не нужен.

Frontend собирается из исходников через `npm ci` / `npm run build`.
Локальные `node_modules` и `dist` исключены из build context.

## Настройка окружения

Из корня репозитория:

```powershell
Copy-Item .env.example .env
notepad .env
```

Не перезаписывайте существующий `.env`. Обязательно заполните:

- `POSTGRES_PASSWORD`: уникальный случайный пароль, желательно 32+ букв/цифр.
- `ADMIN_PASSWORD`: уникальный пароль минимум 12 символов для пустой БД.
- `AI_SERVICE_TOKEN`: длинный случайный секрет, общий для Go и Python.

Для генерации одного секрета средствами PowerShell (без Python/Node):

```powershell
$secretBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($secretBytes)
[Convert]::ToHexString($secretBytes)
```

Сохраните результат только в своём `.env`, повторяя генерацию для каждого секрета.
Файл игнорируется Git и Docker contexts. Не публикуйте `docker compose config`
без `--quiet`, `docker inspect` целиком, cookie jars и скриншоты с секретами.
Секреты передаются только как runtime environment API/Python/PostgreSQL;
frontend их не получает, build args с секретами отсутствуют.

Compose автоматически формирует `DATABASE_URL` с хостом `postgres` из
`POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`. Для автоматического варианта
используйте простые имена пользователя/БД и пароль из букв/цифр. При специальных
символах задайте **полный** `DATABASE_URL`: percent-encode отдельно username и
password (например, `@` → `%40`, `:` → `%3A`, `/` → `%2F`, `%` → `%25`, `#` → `%23`).
`POSTGRES_PASSWORD` при этом содержит исходный пароль, не encoded значение.
Изменение пароля в `.env` не меняет пароль уже созданной роли PostgreSQL:
обновляйте существующую роль отдельно и согласованно, сохраняя данные.

Внутренние адреса и пути Compose фиксированы: `api:8080`, `ai-service:8000`,
`postgres:5432`, `/app/data/documents`. `HTTP_PORT`, `AI_SERVICE_URL`,
`DOCUMENT_STORAGE_DIR` в примере также документируют запуск Go вне Compose;
в Compose они заданы вместе с proxy/volume и не изменяются через `.env`.
Для другого порта браузера меняйте `WEB_PORT`, затем `ALLOWED_ORIGINS`.
Значения вроде `http://localhost:8081` должны совпадать точно, без завершающего `/`.
Корневой `.env` — единственный источник Compose; `ai-service/.env` не читается.

## Сборка и запуск

После заполнения `.env`:

```powershell
docker compose config --quiet
docker compose up --build -d
docker compose ps
```

Откройте http://localhost:8080/login и войдите как `ADMIN_USERNAME` с указанным
паролем. Все запросы браузера идут на тот же origin, включая `/api/v1/...`.
Bootstrap создаёт администратора только в пустой таблице users; повторный запуск
не меняет существующий аккаунт и пароль. Значение `admin` не подходит как пароль.

При ограниченной памяти первую сборку можно выполнить последовательно:

```powershell
docker compose build api
docker compose build ai-service
docker compose build frontend
docker compose up -d
```

Каждый сервис имеет `restart: unless-stopped`. Тяжёлые тесты на старте не запускаются.
Python 3.12 работает с одним Uvicorn worker. PostgreSQL 17 использует
`shared_buffers=128MB`, `max_connections=40`; Go — пул 10/5 соединений.
Модель работает отдельно и не занимает RAM/GPU этого VPS. `ai-service/constraints.txt`
ограничивает PyMuPDF веткой 1.x: ветка 2 добавляет ненужные этому парсеру ONNX/ML-зависимости.
Исходный requirements.txt и Python ingestion не изменены.

## Сервисы и хранение

| Сервис | Назначение | Доступ с host |
| --- | --- | --- |
| frontend | Node 24 LTS build → Nginx, SPA + `/api/` proxy | `127.0.0.1:8080` по умолчанию |
| api | Go 1.27.1, HTTP 8080, миграции и bootstrap | не публикуется |
| ai-service | FastAPI `main:app`, HTTP 8000, X-Service-Token | не публикуется |
| postgres | PostgreSQL 17 Alpine, TCP 5432 | не публикуется |

Внутри стандартной Compose bridge-сети используйте имена сервисов. `localhost`
в контейнере обозначает этот же контейнер. Сеть оставлена с исходящим доступом,
чтобы Python мог обращаться к разрешённой внешней Qwen.

API ждёт `postgres: service_healthy`. Python имеет независимый `/health`, который
не запрашивает Qwen. API не зависит от Python в `depends_on`: Auth, Documents,
Search и существующий RAG fallback продолжают работать при его недоступности.
Новые документы останутся в очереди / будут повторно обработаны по существующим
правилам ingestion. Search требует ранее проиндексированных ready-версий.

Goose SQL 00001–00007 копируются только в build stage и встраиваются через
`//go:embed *.sql` в `migrations/embed.go`. При запуске `/app/server` существующий
код автоматически применяет миграции. В runtime нет SQL-каталога и Goose CLI.
Go и Python работают от пользователя UID 10001. Каталог документов создаётся
с корректным владельцем и mode 0700 до монтирования нового named volume.

Named volumes:

- `postgres_data` → `/var/lib/postgresql/data`.
- `documents_data` → `/app/data/documents`.

Фактические имена имеют префикс `COMPOSE_PROJECT_NAME`, например
`hackathon_postgres_data`. Не меняйте имя проекта для существующих данных без
явного переноса volumes. Обычный `docker compose down` сохраняет volumes;
последующий `up` использует те же данные. Удаление volumes удаляет БД/оригиналы.
Резервируйте PostgreSQL и документы согласованно. Реальные документы не копируются
в образы. Старые volumes `hackathon-local_postgres-data` / `document-data` из
прежнего Compose автоматически не мигрируются и не удаляются.

## Управление

```powershell
docker compose logs --tail 100 api ai-service postgres frontend
docker compose logs -f --tail 50 api
docker compose stop
docker compose start
docker compose down
docker compose up -d
docker compose up -d --build --no-deps ai-service
```

Для обновления Go используйте последнюю команду с `api`, для React — `frontend`.
Nginx повторно разрешает Docker DNS и подхватит новый IP API после пересоздания.
Скрипт `scripts/start-local.ps1` выполняет config/up/ps и также требует только Docker.

## Проверка сервисов и сети

```powershell
docker compose ps
docker compose exec postgres sh -c 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT version_id,is_applied FROM goose_db_version ORDER BY id;"'
docker compose exec api wget -qO- http://127.0.0.1:8080/healthz
docker compose exec api wget -qO- http://ai-service:8000/health
docker compose exec ai-service python -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:8000/health', timeout=3).read().decode())"
docker compose exec frontend nginx -t
curl.exe -i http://localhost:8080/api/v1/auth/me
curl.exe -i http://localhost:8080/login
curl.exe -i http://localhost:8080/chat
curl.exe -i http://localhost:8080/documents
```

Без входа `/api/v1/auth/me` обязан вернуть 401 JSON, а не HTML. Неизвестный
`/api/v1/...` обязан вернуть API 404, а не SPA. React-маршруты получают index.html.
`/healthz` внутри API — liveness: HTTP начинает слушать после миграций/bootstrap;
этот endpoint не проверяет каждую зависимость при каждом запросе.
Для диагностики миграций можно выполнить существующий бинарник:

```powershell
docker compose exec api /app/server migrate status
docker compose exec api sh -c 'test ! -d /app/migrations && id && ls -ld /app/data/documents'
```

## Подключение Qwen

Разрешённый командой endpoint тестового окружения:

```dotenv
LLM_BASE_URL=http://192.168.10.117:8000/v1
LLM_MODEL=qwen3.6-35b-a3b
LLM_ALLOW_REMOTE=true
LLM_TIMEOUT=60
LLM_API_KEY=
```

После изменения `.env` пересоздайте Python: `docker compose up -d ai-service`.
`LLM_ALLOW_REMOTE=true` обязателен для такого адреса. Никаких моделей, Ollama,
GPU-библиотек или inference server в Compose нет. Используйте только разрешённый
endpoint; не подставляйте сторонние облачные сервисы для внутренних документов.

Проверяйте доступность **из Python-контейнера**, не только с Windows:

```powershell
docker compose exec -T ai-service python -c "import os,httpx; u=os.environ['LLM_BASE_URL'].rstrip('/')+'/models'; k=os.environ.get('LLM_API_KEY'); r=httpx.get(u,headers={'Authorization':'Bearer '+k} if k else {},timeout=10,trust_env=False); print('HTTP',r.status_code); r.raise_for_status(); print('configured model present:', any(m.get('id')==os.environ['LLM_MODEL'] for m in r.json().get('data',[])))"
```

Команда не отправляет документы. Успешный `/models` доказывает доступность API
и наличие модели; настоящую генерацию подтверждает только Chat API с источниками.
Если сервер не поддерживает `/models`, отдельно проверьте его контракт и реальную
генерацию на синтетическом документе. Частный адрес требует маршрута из Docker
Desktop / VPN, разрешения firewall inference-сервера и доступности из сети VPS.
VPN Windows может не пропускать Docker/WSL. При проблеме Qwen ingestion и
`/health` продолжают работать, RAG использует штатный search fallback.

## Проверка авторизации, документов и RAG

В браузерных DevTools Network проверьте, что login обращается к
`http://localhost:8080/api/v1/auth/login` и возвращает две Set-Cookie:
`ai_session` (HttpOnly) и `ai_csrf` (доступна JavaScript). Cookies имеют Path=/,
SameSite=Lax; при локальном HTTP `COOKIE_SECURE=false`. Не копируйте их в логи.
После входа `/auth/me` возвращает пользователя. Для POST/DELETE нужны точный
`Origin` и заголовок `X-CSRF-Token` из читаемой cookie; React API client должен
продолжать использовать относительные пути.

Загрузите синтетический PDF/DOCX до 20 MiB, дождитесь ingestion ready, выполните
Search и Chat по его содержимому. Для доступной Qwen ожидается `generated`
с реальными источниками. `search` означает fallback, `no_sources` — отсутствие
подходящих доступных fragments; это не доказательство генерации. Скачайте оригинал,
выполните `docker compose down`, затем `docker compose up -d`; войдите заново,
проверьте документ, ready-статус и совпадение скачанного файла.

Nginx пропускает body до 21 MiB с multipart overhead. API ограничивает сам файл
20 MiB, а весь запрос — плюс 1 MiB. Если меняете лимит API, согласуйте Nginx и
существующий предел Python (20 MiB). Proxy timeouts — 180s; если повышаете
`RAG_TIMEOUT` выше этой границы, также измените proxy timeout и учтите timeout LLM.
Запросы `/api/` не кешируются, Set-Cookie и исходные path/query сохраняются.
Access log исключает query string, Cookie, Authorization и тело запроса.

## VPS и production HTTPS

Меняйте `WEB_BIND_ADDRESS` и `WEB_PORT` для размещения рядом с внешним reverse
proxy; при proxy на том же host оставьте bind `127.0.0.1`. Открывать PostgreSQL,
Python и API наружу не требуется. Укажите реальные HTTPS origins. Настройте
TLS, firewall, резервные копии и ограниченный доступ к секретам. Для браузера
нужен один HTTPS origin и Secure HttpOnly cookie.

**Ограничение существующего backend:** `APP_ENV=production` требует
`COOKIE_SECURE=true` и собственные `HTTP_TLS_CERT_FILE` / `HTTP_TLS_KEY_FILE`;
router проверяет реальный `r.TLS`, не доверяет X-Forwarded-Proto.
Текущий Compose/Nginx рассчитан на локальный HTTP и не монтирует сертификаты.
Простое переключение APP_ENV/COOKIE_SECURE или только внешний TLS-terminating
proxy не даст работающий production. Для production нужна отдельная подготовка
в рамках этого же единственного Compose: read-only mount сертификатов в API,
передача TLS-переменных и HTTPS proxy_pass в Nginx с проверкой сертификата upstream
(`proxy_ssl_verify on`, доверенный CA и корректное server name), а также внешний
HTTPS listener. Не отключайте проверки Auth/CSRF/TLS. Подробности: [auth.md](auth.md).
Изменение модели доверенных proxies требует отдельного изменения приложения.
Rate limiter Go сейчас считает адрес Nginx общим peer IP; внешнему proxy также
потребуется ограничение частоты login. В этой задаче бизнес-логика не меняется.

## Troubleshooting

| Проблема | Что проверить |
| --- | --- |
| Docker daemon недоступен | Docker Desktop запущен, Linux/WSL 2 engine, `docker version` |
| `package.json: not found` | Восстановить реальные исходники frontend; node_modules/dist недостаточно |
| Compose требует секрет | Заполнить корневой `.env`; проверить `config --quiet` |
| API не стартует | `logs api`, БД healthy, пароль роли, DATABASE_URL и bootstrap пароль ≥12 символов |
| API не может писать файлы | Используется нужный documents_data, UID 10001 владеет каталогом; не подменён root-owned bind mount |
| Python unhealthy | `logs ai-service`, непустой AI_SERVICE_TOKEN, корректный LLM URL и LLM_ALLOW_REMOTE |
| 401 после входа | Cookies для текущего host, Path=/, HttpOnly session, COOKIE_SECURE=false на HTTP; не смешивать localhost/127.0.0.1 |
| 403 / CSRF | Точный ALLOWED_ORIGINS с портом, Origin на login, X-CSRF-Token на writes, актуальная сессия |
| 429 на login | Существующий лимит 10 попыток / 15 минут на peer IP; за Nginx он общий |
| 502 | API запущен и слушает 8080, nginx -t, Docker DNS; `logs api frontend` |
| 413 | Файл ≤20 MiB и multipart ≤21 MiB; согласованные лимиты Go/Python/Nginx |
| RAG mode=search | Qwen из контейнера, правильный model ID, LLM_TIMEOUT/RAG_TIMEOUT, `logs api ai-service` |
| Search пуст | Ingestion ready, active version и ACL; без chunks поиск не найдёт текст |
| БД «пропала» | Не изменился ли COMPOSE_PROJECT_NAME и префикс volumes; не выбран ли новый volume |
| Пароль .env не применяется | Bootstrap не сбрасывает существующего admin; PostgreSQL env инициализирует только пустой volume |
| Порт занят | Изменить WEB_PORT вместе с ALLOWED_ORIGINS или остановить предыдущий локальный процесс |
| Qwen работает с host, но не в Docker | Маршрут Docker/WSL/VPN, private LAN и firewall; выполнить проверку `/models` внутри ai-service |

## Реальные проверки этого изменения

Результаты выполненных команд и ограничения проверки записаны в
[docker-validation.md](docker-validation.md). Они относятся к текущему checkout,
а не гарантируют доступность частного inference endpoint из другой сети.
