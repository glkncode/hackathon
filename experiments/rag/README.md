# Локальный эксперимент FTS / e5 / Hybrid

Исторические `results/2026-10-24*` перенесены во внешний архив, проверенный
по SHA-256: [отчёт об уборке](../../docs/repository-cleanup-report.md).
Для проверки старой разметки сначала восстановите нужный каталог из архива.
Команда `export` может вновь создать локальную копию результатов; runtime их не читает.

Эксперимент отделён от приложения: новые Go/Python production endpoints, миграции,
Compose, публичные DTO и системные промпты не изменяются. `embedding.py` не
импортируется основными сервисами. Веса загружаются только отдельной явной командой.

## Что сравнивается

- **FTS:** настоящий `POST /api/v1/search` текущего Go image, включая уже существующие
  deterministic Query Understanding, strict/relaxed SQL и table expansion.
- **Semantic:** exact cosine similarity нормализованных e5-векторов на CPU.
  Поиск только в разрешённом SQL snapshot действующих ready-версий.
- **Hybrid:** исходный FTS top-10 + semantic top-10, RRF `sum(1/(60+rank))`, top-10,
  дедупликация по настоящему chunk ID. Без словарей синонимов, переписанных запросов,
  reranker или подбора порога по holdout.

`corpus.json` содержит 9 доступных документов разных тематик и 1 закрытый документ;
исходные файлы генерируются в `.local-integration/hybrid/documents`. Это только
синтетические документы. Они загружаются через реальный Go API и проходят текущий
Python ingestion. У командировочного документа сначала публикуется историческая
версия, затем актуальная. Идентификаторы назначает PostgreSQL; до baseline
`manifest.json` фиксирует **фактические** chunk/document/version IDs и gold labels.
При новом запуске IDs будут другими, но правила разметки остаются теми же.

Development: 4 документа, 16 положительных + 4 отрицательных вопроса. Holdout:
5 документов, 20 положительных + 6 отрицательных вопросов. Два вопроса сравнивают
несколько документов; один содержит несколько условий внутри документа. Историческая
версия и закрытый документ не входят в embedding pool. Holdout не используется для
подбора параметров. Сейчас параметры вообще не обучаются/не подбираются на разметке.

## Воспроизведение (PowerShell)

Не запускать в production. Скрипты используют только фиксированные имена отдельных
контейнеров, loopback-порты 18081/18083/55437 и БД `search_test`. Основной Docker
Compose и существующие контейнеры не заменяются. Нужны Docker, уже собранные локальные
Go/Python images и разрешённая конфигурация Qwen в `hackathon-ai-service-1`.
Служебный токен переносится через временный игнорируемый файл и удаляется в `finally`;
в результаты он не попадает. Не публикуйте `.local-integration` целиком.

```powershell
python -m venv .local-integration/rag-venv
$py = '.local-integration/rag-venv/Scripts/python.exe'
& $py -m pip install -r experiments/rag/requirements.txt

# Явные действия оператора, не Docker build и не startup основных сервисов.
docker pull pgvector/pgvector:pg17
& $py experiments/rag/download_model.py --output .local-integration/e5-small
./experiments/rag/start-local.ps1 -Action start
# Дождаться http://127.0.0.1:18081/healthz и http://127.0.0.1:18083/health.

& $py experiments/rag/benchmark.py prepare
& $py experiments/rag/benchmark.py baseline
& $py experiments/rag/benchmark.py index
& $py experiments/rag/benchmark.py compare
& $py experiments/rag/benchmark.py answers
& $py experiments/rag/benchmark.py probe
& $py experiments/rag/benchmark.py verify
& $py experiments/rag/benchmark.py environment
& $py experiments/rag/benchmark.py diagnostics

$env:RAG_EXPERIMENT_MODEL_PATH = (Resolve-Path .local-integration/e5-small).Path
& $py -m pytest experiments/rag -q
./experiments/rag/start-local.ps1 -Action stop
```

`prepare` отказывается перезаписывать frozen manifest. `compare` отказывается повторно
оценивать тот же holdout после появления `frozen-parameters.json`. Для нового исследования
нужны новый корпус и отдельный каталог/БД, а не удаление этих защит ради подбора результата.
`answers` сохраняет каждый полученный ответ и позволяет продолжить после сбоя, не вызывая
модель повторно для успешных записей. Ошибки HTTP сохраняются отдельно; нельзя исключать
их из знаменателя при оценке качества. Модель при temperature=0 всё равно может отвечать
по-разному; успешный повтор не отменяет предыдущую ошибку.

После содержательной проверки новых ответов нужно самостоятельно создать новый
`review.json` с SHA256 наблюдений и явными verdicts; пример схемы — в сохранённых
`results/2026-10-24/review.json`. `review_results.py` проверяет hash, полноту и source-ID
membership, затем считает reviewed metrics. Старую разметку к изменённым ответам
применять нельзя. Команда `benchmark.py export` копирует только allowlist synthetic
JSON artifacts и отказывается экспортировать известные private credentials/endpoint.

`verify` останавливает/перезапускает **только экспериментальные** контейнеры. Проверяет
скачивание доступных оригиналов, 404 для закрытого, отзыв/возврат ACL, работу Go FTS при
остановленном Python и сохранение chunk IDs после рестарта. Данные остаются в остановленных
контейнерах и локальном каталоге. Скрипт не удаляет volumes или основные сервисы.

## Модель, токены, идентичность индекса

- `intfloat/multilingual-e5-small`, pinned HF revision
  `614241f622f53c4eeff9890bdc4f31cfecc418b3`, исходный FP32 ONNX, CPUExecutionProvider,
  2 intra-op threads / 1 inter-op thread.
- `query: ` для вопросов, `passage: ` + название документа + исходный content для
  фрагментов. Тест проверяет равенство token IDs с полной строкой, включая XLM-R
  word-boundary markers. Mean pooling с attention mask, L2 normalization, 384 значения.
- Ни одна последовательность не превышает 512 токенов с префиксом и BOS/EOS.
  Длинные passages делятся на окна с перекрытием 64 токена; сохраняются token spans
  и **исходный chunk ID**, score чанка — max cosine его окон. Слишком длинный вопрос
  явно отклоняется. Исходные чанки в PostgreSQL не меняются.
- Model ID, revision, SHA256 весов/токенизатора, размерность, formatting revision,
  content/index revision, readiness и window-to-chunk mapping записываются в `index.json`.
  Query/index mismatch, изменившийся ACL snapshot и неизвестные source IDs отклоняются.
- Отдельный long-context probe содержит 2267 токенов. Проверяются все окна и хвост,
  а не только первые 512. Это не дополнительный вопрос для подгонки retrieval.

## Как читать метрики

- Recall@k — доля всех размеченных gold chunk IDs в top-k, затем macro average по
  положительным вопросам; для составного вопроса нужны оба источника.
- MRR — обратный ранг первого gold chunk, среднее по положительным вопросам.
- Candidate precision — micro precision по **минимальным подтверждающим gold chunks**
  среди всех возвращённых кандидатов, включая отрицательные вопросы. Подписи/шапки
  таблиц и дополнительный правдивый контекст не размечены как минимальные ответы,
  поэтому это строгая evidence precision, а не тематическая близость.
- `negative_candidate_rate` — частота выдачи кандидатов для вопросов без ответа.
  Это отдельная retrieval-ошибка; она не равна ложному содержательному ответу LLM.
- В raw `answers.json` остаются текст ответа, цитаты и входные IDs. Статус generated/
  answered и substring-проверки **не доказывают** grounded accuracy. Проверенная оценка
  должна сопоставить каждое утверждение с текстом и IDs; см. `review.json` и отчёт.
- Проверка цитат по gold IDs строже смысловой проверки: шапка таблицы может быть
  полезной цитатой, хотя не входит в минимальный gold. Не смешивать эти метрики.
- Retrieval latency — median трёх warm измерений на вопрос, затем p50/p95 по вопросам.
  FTS включает loopback HTTP/Go/PostgreSQL. Semantic включает токенизацию, ONNX и cosine
  scan в локальном процессе. Hybrid — **последовательная оценка суммы** этих времён и
  измеренного RRF; это не измерение готового hybrid HTTP endpoint.
- Для генерации FTS проходит настоящий Chat API; экспериментальные semantic/hybrid arms
  передают разрешённые top-8 в существующий Python RAG, без изменения его промпта.
  Для follow-up исходная история получена через настоящий Chat API, одинаковая история
  передаётся в experimental generation. Retrieval во всех arms использует текущий
  вопрос. Нового contextual planner в этом эксперименте нет.
- RAM — sampled peak RSS процесса (20 ms); CPU — process CPU time / wall time.
  Docker `stats` даёт отдельные наблюдения контейнеров. Windows host с 12 logical CPUs
  и 32 GiB RAM не является измерением VPS 2 vCPU / 4 GiB; 2 ONNX threads не симулируют
  полностью ограничения такого VPS. Размер vector corpus здесь слишком мал для
  выводов о больших объёмах или необходимости HNSW.

Экспериментальные векторы — локальный snapshot, не production authorization cache.
Перед запуском compare/answers проверяется актуальный SQL snapshot; в рабочем backend
такой memory-only индекс использовать нельзя. План SQL-интеграции с ACL находится
в [integration-plan.md](integration-plan.md).
