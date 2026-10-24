# Проверка semantic search — 24 октября 2026

Реализован работающий вертикальный pilot за выключенным по умолчанию feature flag.
E5 выполняется на CPU в существующем FastAPI, векторы сохраняются в PostgreSQL 17
с pgvector, Go выполняет SQL-поиск с ACL и проверяет достаточность подтверждений
перед существующей генерацией Qwen. Frontend и публичные DTO сохранены.

## Проверки, которые выполнены

- `docker compose config --quiet` и сборка `api`, `ai-service`: успешно.
  Веса не скачивались при сборке.
- `go test ./...` с `TEST_DATABASE_URL`, указывающим на отдельный PostgreSQL 17 /
  pgvector: успешно. Включены реальные миграции, repository/ACL/version/history,
  readiness, несовместимая revision, lease recovery, идемпотентность,
  FTS fallback, отмена контекста и ошибки HTTP/embedding-протокола.
- Дополнительно прошли новые проверки конфигурации и атомарного сохранения /
  max-cosine нескольких окон с дедупликацией исходного chunk ID.
  Проверены нормализация Unicode-пробелов в цитатах, отклонение пустой цитаты
  и совместимость отрицательного cosine с неотрицательным score старого Chat DTO.
- `go vet ./internal/... ./cmd/...`: успешно.
- Python: **142 passed**, с настоящими pinned ONNX-весами в read-only mount.
  Проверены префиксы и токенизация, batch против исходного inference,
  нормализация/384/NaN, long context и хвост, лимиты, отсутствие модели,
  единственная загрузка, service auth, timeout без освобождения занятого CPU-слота,
  проверки цитат и пробельных символов PDF. Ничего не скачивалось тестами.
- В общем Go-прогоне Windows symlink-тест пропущен из-за отсутствия поддержки
  создания symlink на этом хосте. Опциональный `TestIntegrationWorkingGroupQwen`
  сначала был пропущен без переменных, затем **отдельно запущен с настоящими
  Python/Qwen и прошёл** (44,65 с). Его пропуск не выдан за успешное выполнение.
- Отдельно проверен запуск миграций на чистом stock `postgres:17-alpine`:
  миграция останавливается с явным сообщением `pgvector is unavailable`, именем
  нужного PostgreSQL 17 image и ссылкой на процедуру сохранения volume.
- Реальный Docker E2E прошёл ingestion, durable indexing, Go Search и Go Chat,
  LAN Qwen, проверку цитат, отзыв ACL и скрытие истории, FTS/Chat при остановленном
  Python, перезапуск БД и сохранность vector/source IDs.
- Дополнительный неизвестный заранее длинный источник: **2154 токена, 5 окон**
  одного реального chunk ID; проверены overlap, все нормы/размерности, последний
  token span и нахождение факта из хвоста через Go semantic search.

## Финальный независимый набор

Второй новый набор: 4 документа, 12 положительных и 6 отрицательных вопросов;
раньше эти документы и вопросы не оценивались. Форматы PDF/DOCX/TXT.
Корпус поиска: 56 разрешённых чанков, 18 активных документов с учётом одного
закрытого, плюс сохранённая историческая версия. Все реальные IDs и разметка
зафиксированы до retrieval в `experiments/semantic-pilot/results/2026-10-24-final/manifest.json`.

| Метод | Recall@5 | Recall@10 | MRR | Precision кандидатов | Grounded ответы | Корректность цитат | Ложные ответы на negatives |
|---|---:|---:|---:|---:|---:|---:|---:|
| FTS | 0% | 0% | 0 | —, кандидатов нет | 0/12 | — | 0/6 |
| Semantic + evidence gate | 91,7% | 91,7% | 0,917 | 100% | 11/12 | 11/11 | 0/6 |
| Semantic + FTS + тот же gate | 91,7% | 91,7% | 0,917 | 100% | 11/12 | 11/11 | 0/6 |
| Raw semantic, до gate | 100% | 100% | 1 | 6,7% | не передавался напрямую в Chat | — | top-k выдал кандидатов для 6/6 |

Grounded correctness проверена агентом по утверждениям и конкретным положениям
исходных чанков, затем проверены IDs цитат и hash наблюдений. Это не независимая
экспертиза человеком. Все **54** финальных ответа, включая отказы, сохранены в
`answers.json`; вердикты — `review.json`, расчёт — `review-summary.json`.
Один false negative (`f03`) сохранён: достаточное положение было в raw retrieval,
но строгий gate отказался пропускать его дальше. Правила под этот вопрос не менялись.

Старый holdout использовался как регрессионный: FTS Recall@10 **20%**, raw E5
**90%**, semantic после gate **90%**. Самостоятельная польза добавления FTS снова
не подтверждена; обязательного RRF нет.

Первый новый набор выявил проблему сравнения цитат из PDF с NBSP/переносами:
raw recall 100%, после строгой посимвольной проверки 75%. Исправлена только
нормализация пробельных символов, с тестом на сохранение слов/чисел/отрицаний.
Первый результат сохранён отдельно в `results/2026-10-24`, не заменён улучшенным.
Финальные числа получены на следующем, ранее не использованном наборе.

## Latency и ресурсы

| Путь | Search p50/p95 | Chat p50/p95 |
|---|---:|---:|
| FTS | 6,4 / 15,1 мс | 10,8 / 18,2 мс, без генерации при отсутствии источников |
| Semantic + gate | 2,16 / 2,89 с | 4,03 / 5,03 с |
| Semantic + FTS + gate | 1,50 / 2,39 с | 3,36 / 4,60 с |

Это по одному warm-измерению на вопрос, порядок arms фиксирован; Qwen prefix
cache и сеть влияют на разницу. Преимущество hybrid по скорости не доказано.
Raw semantic: p50/p95 **218 / 291 мс**, включая E5 HTTP и Windows `docker exec psql`;
это диагностическое измерение, его нельзя приравнивать к latency чистого SQL.

Docker Desktop реально предоставлял **15,55 GiB RAM / 12 CPU**. Sampled peak
working memory: Python **874,6 MiB**, PostgreSQL **126,4 MiB**, semantic Go API
**48,24 MiB**. Sampled CPU peak: Python **13,89%**, PostgreSQL **51,32%**, Go
**1,70%**, где 100% — одно ядро. Частично одновременно выполнялись integration
tests в отдельных схемах того же тестового PostgreSQL; это наблюдения ресурсов,
не изолированный нагрузочный benchmark и не гарантированный абсолютный пик RSS.
Ресурсы удалённого Qwen не измерялись.

Четыре финальных документа достигли готовности индекса примерно за **4,8–6,2 с**
от постановки задачи, включая polling, загрузку CPU-модели и конкуренцию двух
Go pilot arms. Точные per-version timings — `indexing.json`. После финальной
оценки отдельно добавлен long-context probe; он не включён в метрики качества.

## Файлы этого изменения

Созданы:

- `migrations/00008_semantic_embeddings.sql`.
- `ai-service/app/embeddings/{__init__,onnx,model,schemas,routes,evidence}.py`.
- `ai-service/scripts/download_embeddings.py`, `ai-service/tests/test_embeddings.py`.
- `internal/domain/embedding.go`, `internal/aiclient/embeddings.go` и его тесты.
- `internal/repository/postgres/{embedding_repository,semantic_search_repository,semantic_integration_test}.go`.
- `internal/service/{semantic_worker,semantic_search}.go` и соответствующие тесты.
- `internal/config/semantic_test.go`, `scripts/semantic-pilot.ps1`.
- `experiments/semantic-pilot/`: два корпуса, evaluator, проверка review, README,
  frozen результаты и ресурсные наблюдения.
- `docs/semantic-search.md`, этот отчёт.

Дополнены `ai-service/app/application.py`, `ai-service/app/core/config.py`,
`ai-service/requirements.txt`, `ai-service/Dockerfile`, `internal/aiclient/client.go`,
`internal/config/config.go`, `internal/service/search.go`, `cmd/server/main.go`,
`docker-compose.yml`, `.env.example`. В двух старых repository-тестах обновлены
предположения о последней миграции/откате именно миграции 00007.

Незакоммиченные изменения, существовавшие до работы, сохранены; этот список
отделяет новую интеграцию от них. Миграции 00001–00007, frontend, CI/CD и VPS
не изменялись. Commit, push, deploy и `docker compose down -v` не выполнялись.

## Запуск и границы готовности

Контракт API, структура индекса, SQL ACL, fallback, настройки и команды описаны
в [semantic-search.md](semantic-search.md). Веса — постоянный Compose volume;
первоначальная загрузка только явной командой, не во время build/startup.

Рабочая БД осталась в исходном контейнере `postgres:17-alpine`; тесты использовали
отдельный pgvector PostgreSQL 17.11 с отдельными named volumes. Совместимость
исходного Alpine volume с Debian image **не проверялась на рабочих данных**:
перед первым переключением основного Compose нужно выполнить документированную
процедуру backup и проверки копии volume. Исходные volumes не удалялись.

Это законченный локальный pilot с консервативным gate и FTS fallback. Малый
синтетический набор, дополнительные Qwen-вызовы и единичный оставшийся отказ
не дают основания объявлять production acceptance для любых документов или
нагрузки. По умолчанию semantic search остаётся выключенным.

После проверки последней сборки pilot-контейнеры остановлены, их PostgreSQL и
document volumes сохранены. Повторный запуск: `./scripts/semantic-pilot.ps1`.
Проверки последней сборки и её image IDs сохранены в `validation.json` результатов.
