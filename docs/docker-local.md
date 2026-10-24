# Устаревший локальный запуск

Актуальная инструкция: [docker.md](docker.md).

Используется единственный корневой `docker-compose.yml` с сервисами `postgres`,
`ai-service`, `api`, `frontend`. Скрипт `scripts/start-local.ps1` вызывает тот же
Compose и требует только Docker Desktop и заполненный корневой `.env`.
Старые `.local-integration/docker.env` и `ai-service/.env` автоматически не читаются.
Старые volumes остаются на диске; переход на новые volumes не переносит данные.
