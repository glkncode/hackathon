# Проверка Docker-конфигурации — 24 октября 2026

Проверено на Windows с Docker Desktop (Linux amd64), Docker Engine 29.8.0,
Compose v5.5.1. Использовался отдельный проект `hackathon-docker-check` и
игнорируемый `.local-integration/docker-check.env` со случайными временными
секретами. Корневой `.env` не создавался и не перезаписывался. Документ тестовый,
синтетический; пользовательские документы модели не передавались.

## Итог

| Проверка | Фактический результат |
| --- | --- |
| Compose config | `config --quiet` завершился успешно; ровно четыре сервиса и один Compose-файл |
| Docker build | Go, Python и frontend собраны из исходников; общая повторная сборка успешна |
| Go | Go 1.27.1 builder, Linux amd64, Alpine runtime, UID 10001 |
| Python | Python 3.12, `main:app`, один Uvicorn worker; ONNX Runtime и NumPy отсутствуют |
| PostgreSQL | 17 Alpine, healthy; SQL-проверка подтвердила применённые версии 1–7 |
| Миграции | В пустой БД автоматически применены все семь; SQL-файлов в `/app/migrations` runtime нет |
| Bootstrap | Администратор создан автоматически; login успешен; после пересоздания сохранён |
| Health | Все четыре контейнера healthy; API `/healthz`, Python `/health` отвечают 200 |
| Независимость health от Qwen | FastAPI TestClient с пустыми LLM_BASE_URL/LLM_MODEL получил 200 на `/health` |
| Go → Python | `wget http://ai-service:8000/health` из API успешен |
| Внутренняя аутентификация | Запрос к Python ingestion без X-Service-Token вернул 401 |
| Nginx | `nginx -t` успешен; `/`, `/login`, `/chat`, `/documents` отдают React index |
| API proxy | Анонимный `/api/v1/auth/me` → 401 JSON; неизвестный `/api/` → 404 без SPA fallback |
| Query string | `/documents?limit=1&offset=0` через Nginx вернул ровно один документ |
| Cookies | Login через localhost:8080 сохранил две Set-Cookie; session HttpOnly, CSRF читаемая; запрос без CSRF → 403 |
| Браузер | Реальный вход тестового member через React; показан ответ Qwen и карточка источника |
| PDF ingestion | PDF создан в Python-контейнере, загружен через публичный Nginx, обработан настоящим ingestion до ready |
| Search | Загруженный PDF найден, возвращены реальные источники |
| Qwen из контейнера | `/v1/models` → HTTP 200; qwen3.6-35b-a3b присутствует |
| RAG | Реальный Chat API вернул `mode=generated`, один источник; UI показал ответ о семи годах хранения |
| Отказ Python | После `stop ai-service` Auth/Documents/Search работают; Chat → `mode=search` с источниками |
| Persistence | После `down` и `up -d --wait` сохранены аккаунты, ready-документ и оригинал с прежним SHA-256 |
| Публикация портов | Только frontend `127.0.0.1:8080:80`; api/ai-service/postgres — без host bindings |
| Go проверки | `go test ./...`, `go vet ./...` завершились успешно; отдельно пройден пакет HTTP |
| Python проверки | 110 тестов прошли в production-образе с read-only mount tests/examples и `--network none` |
| Секреты | `.env` и временный env игнорируются Git; frontend не получает AI_SERVICE_TOKEN |

Снятый в браузере ответ после пересоздания контейнеров сохранён локально в
`.local-integration/docker-chat-check.jpg` (не коммитится).

## Выполненные команды

Для каждой Compose-команды использовался явный временный env:

```powershell
docker compose --env-file .local-integration/docker-check.env config --quiet
docker compose --env-file .local-integration/docker-check.env build api
docker compose --env-file .local-integration/docker-check.env build ai-service
docker compose --env-file .local-integration/docker-check.env build frontend
docker compose --env-file .local-integration/docker-check.env build
docker compose --env-file .local-integration/docker-check.env up --build -d
docker compose --env-file .local-integration/docker-check.env up -d
docker compose --env-file .local-integration/docker-check.env ps
docker compose --env-file .local-integration/docker-check.env down
docker compose --env-file .local-integration/docker-check.env up -d --wait --wait-timeout 90
```

Тесты Python использовали уже собранный образ; тестовые файлы и примеры
подключались read-only только на время запуска, без включения в образ.
Первый запуск tests без examples выявил отсутствие fixture; после подключения
examples вся исходная suite прошла. Тесты и приложение не менялись ради проверки.
Go suite запускалась без специально выделенной TEST_DATABASE_URL; отдельные
DB integration tests, требующие этой переменной, здесь не заявлены как выполненные.
Реальная БД проверена описанным HTTP E2E и запросом таблицы миграций.

## Найденные и исправленные проблемы

- Прежний compose.yaml запускал три сервиса, публиковал Python и блокировал API
  зависимостью от Python. Он заменён единственным docker-compose.yml с четырьмя
  сервисами и публикацией только Nginx.
- Сначала frontend исходники отсутствовали; позднее появились в рабочем каталоге.
  Компоненты не изменялись. Lock-файл сформирован чистым Node-контейнером, без
  Windows node_modules: первоначальный вариант пропускал Linux optional dependencies.
- Свободная зависимость PyMuPDF выбирала 2.0 и устанавливала ONNX/ML-пакеты.
  Docker использует requirements.txt с constraints.txt (`PyMuPDF<2`); фактически
  установилась 1.28.2, с которой прошли тесты и реальная обработка PDF.
- Значение ADMIN_PASSWORD=admin не соответствовало существующему минимуму
  12 символов. В примере пароль оставлен пустым для самостоятельного заполнения.
- Старый PowerShell launcher требовал локальный Python/venv. Теперь он запускает
  тот же корневой Compose и использует только Docker Desktop и заполненный .env.

## Что остаётся проверить в целевом окружении

- Маршрутизацию до частного Qwen IP из конкретного VPS/VPN: текущая проверка
  подтверждает только эту Windows/Docker сеть.
- Production HTTPS, Secure Cookie и сертификаты до Go: текущая локальная схема
  использует HTTP, а существующий backend требует реальный TLS в production.
  Порядок подготовки и ограничения описаны в [docker.md](docker.md).
- Нагрузку на VPS 2 vCPU / 4 GB, параллельные большие PDF/DOCX и медленные upload.
  Граничная загрузка ровно 20 MiB и нагрузочный тест не выполнялись. Лимиты
  Nginx/Go/Python проверены по конфигурации; E2E выполнен на небольшом PDF.
- Собственные рабочие PDF/DOCX, резервное копирование и восстановление данных.

Секреты, тестовые аккаунты и документы относятся только к отдельному проверочному
проекту. Его volumes сохранены; они не используются проектом `hackathon` из
.env.example. Проверочный стек после окончания работы остановлен с сохранением
volumes, чтобы освободить порт 8080. Commit, push и публикация на VPS не выполнялись.
