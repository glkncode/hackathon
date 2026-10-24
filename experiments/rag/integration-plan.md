# План интеграции после локального сравнения

Это план, не выполненная интеграция. Миграция 00008, endpoints planner/reranker,
embedding workers и новый Go retrieval pipeline в рамках эксперимента не добавляются.

Измерения подтверждают пользу semantic candidate retrieval относительно нынешнего
FTS на development и на пяти новых holdout-тематиках. Дополнительная польза RRF
относительно одного e5 на этом корпусе не подтверждена. Top-k сам по себе не является
доказательством: для всех отрицательных запросов он возвращает похожие фрагменты.
Разумный следующий шаг — ограниченный pilot с качественными gates и FTS fallback.

## 1. Аддитивный индекс и отдельный embedding worker

1. Проверить расширение pgvector на целевой PostgreSQL 17; штатный `postgres:17-alpine`
   в текущем стеке его не содержит. Установка расширения должна быть явной операцией
   подготовки окружения. Не менять старые миграции или удалять `document_chunks`.
2. Новая миграция после 00007 создаёт метаданные index/model revisions и отдельную
   таблицу окон. В каждой записи: `index_id`, реальный `chunk_id`,
   `document_version_id`, номер окна, token spans, dimension и вектор. Нужна составная
   FK `(chunk_id, document_version_id)` на уникальную пару исходной таблицы: нельзя
   связать embedding чанка A с версией B. Исходный GIN FTS остаётся без изменений.
3. Model revision включает название и immutable revision, dimension, SHA256 tokenizer,
   pooling, normalization, prefixes, window size/overlap и формат документа. Совпадения
   только display name модели недостаточно. Несовместимые индексы не объединяются.
4. Индексация — отдельная ограниченная очередь с lease/retry и per-version readiness.
   После ingestion и для существующих active-ready версий ставится durable job.
   Отказ embeddings не переводит уже готовый FTS-документ в failed. Новая версия
   недоступна в semantic ветке до полного успешного завершения её индексации.
5. Контролируемый reindex строит новый revision рядом со старым, проверяет полноту,
   качество и размерность, затем атомарно переключает configured active index.
   Старый индекс сохраняется для rollback. Нет перезаписи исходных chunks.
6. Для e5 фиксировать 384 dimension, L2 norm и cosine; другие dimensions допускаются
   только с совместимой новой схемой/метаданными. Отдельные окна отображаются обратно
   на source chunk ID; chunk score — max cosine, без выдуманных source IDs.

## 2. Exact semantic SQL с ACL до выдачи текста

Пример формы запроса (имена будущих таблиц условные; SQL не установлен в приложении):

```sql
WITH allowed AS MATERIALIZED (
    SELECT c.*, d.id AS document_id, d.title, v.version_number
    FROM document_chunks c
    JOIN document_versions v ON v.id = c.document_version_id
    JOIN documents d ON d.id = v.document_id AND d.active_version_id = v.id
    JOIN users u ON u.id = $1 AND u.is_active AND u.role = $2
    WHERE v.status = 'ready'
      AND (u.role = 'admin' OR EXISTS (
          SELECT 1 FROM document_access a
          WHERE a.document_id = d.id AND a.user_id = u.id
      ))
), candidates AS (
    SELECT c.id, min(w.embedding <=> $3::vector) AS distance
    FROM allowed c
    JOIN embedding_windows w ON w.chunk_id = c.id
       AND w.document_version_id = c.document_version_id
    JOIN embedding_version_state s ON s.index_id = w.index_id
       AND s.document_version_id = c.document_version_id AND s.status = 'ready'
    WHERE w.index_id = $4 AND w.dimension = $5
    GROUP BY c.id
    ORDER BY distance, c.id
    LIMIT $6
)
SELECT c.*, 1.0 - r.distance AS similarity
FROM candidates r JOIN allowed c ON c.id = r.id
ORDER BY r.distance, c.id;
```

Идентичность пользователя, модель, размерность и limit задаёт Go, не LLM. Векторы
передаются параметрами, SQL из текста пользователя не генерируется. Каждая FTS/semantic
ветка самостоятельно применяет ACL/active-ready. Ограниченные кандидаты повторно
валидируются перед внешним reranking/generation и при сохранении Chat turn; существующий
`SaveMessageWithSources` и отзыв provenance в истории сохраняются. Глобальный поиск
по закрытым документам с последующей фильтрацией в Go недопустим.

Начать с exact search и EXPLAIN/latency на репрезентативном количестве окон.
HNSW/IVFFlat добавлять только после измерений; ANN+ACL может менять recall и требует
отдельных тестов. Current 32 chunks не обосновывают ANN.

## 3. Private inference contracts и ограничение ресурсов

- Embedding API обслуживается отдельным разрешённым inference сервером или явно
  включённым CPU-сервисом. Ничего не скачивать при Docker build/startup. Успех локального
  Windows эксперимента не является разрешением размещать модель рядом с PostgreSQL.
- Embedding contract: input kind, bounded texts, ожидаемый immutable model revision;
  ответ — тот же revision/dimension и конечные нормализованные vectors в исходном
  порядке. Проверять cardinality, размер, NaN/Inf, norm, HTTP timeouts и максимальный
  response body. На ошибке — FTS, без частичного индекса.
- Qwen остаётся chat-моделью: её `/embeddings` вернул 404. Не направлять туда embedding
  запросы. Измеренный local RSS примерно 1.1 GiB и 2 CPU threads требует дополнительного
  Linux/VPS нагрузочного измерения, учёта concurrent requests и bounded batch sizes.

## 4. Planner, fusion, reranking и evidence gate

1. Сначала минимальная semantic ветка с feature flag off по умолчанию. При сбое
   embedding inference возвращать нынешний FTS результат. Публичные Search/Chat DTO
   остаются прежними; источники сохраняют все реальные document/version/chunk IDs.
2. Затем приватный `/internal/v1/query/plan`: current question + ограниченная история,
   полученная через существующую ACL-redacted `RecentMessages`. Строгий JSON:
   `standalone_question`, максимум 2–3 `search_queries`, `needs_clarification`.
   Сохранять исходный вопрос отдельной веткой. Запретить SQL, document facts и ACL
   решения. Имена/числа/даты/отрицания/условия нельзя терять при переформулировке.
   Сбой/таймаут/невалидный JSON — исходный вопрос. Документы и история — untrusted data,
   не system instructions. Измерить отдельно contextual queries и дополнительную latency.
3. Небольшие независимые FTS/semantic pools, дедупликация по chunk ID, RRF k=60.
   Diversity policy должна сохранять несколько релевантных документов; нельзя полностью
   забить бюджет однотипными кусками одного файла. Подбирать её на новом development
   наборе с multi-document ground truth, не на уже просмотренном holdout.
4. Нужен evidence gate: semantic similarity не отвечает на вопрос о наличии доказательств.
   У development positives/negatives cosine существенно перекрывается; один глобальный
   cutoff без оценки precision/false positives проблему не решает.
5. Отдельный reranker оценивает максимум 20–30 **уже разрешённых** кандидатов по
   исходному/standalone вопросу, с ограничением bytes. Можно сравнить специализированный
   multilingual reranker и Qwen с контрактом `{chunk_id, relevance}` / `no_evidence`.
   Строго проверить исходный allowlist IDs, уникальность, score range и response size.
   При ошибке reranker не объявлять произвольный semantic top-k доказательствами;
   безопасный fallback — проверенный FTS или недостаточность сведений.
6. После отбора допускается bounded SQL expansion: строки одной таблицы, её шапка/
   подпись, продолжение пункта. Только тот же разрешённый документ и версия, реальные
   IDs, явная структурная граница. Не расширять через обычный соседний пункт и не
   считать ненадёжный унаследованный `section_title` достаточным признаком таблицы.
   Нынешнее expansion внутри FTS репозитория нужно аккуратно выделить, сохранив старый
   fallback и regression tests.

## 5. Условия перехода к основному backend

Сначала новый, ранее не просмотренный holdout и целевые Linux 2 vCPU / 4 GiB:
Recall@5/10, MRR, candidate/evidence precision, manually reviewed grounded accuracy,
false factual answer rate и protocol abstention rate отдельно; p50/p95, peak RAM/CPU
под concurrency и на длинных документах. Не считать `mode=generated` правильностью.
Проверять ACL revoke/history redaction, stale roles/inactive users, публикацию v2,
отсутствие исторических/failed/processing версий во всех ветках, неизвестные source IDs,
исчезновение inference и restart/reindex recovery. Не принимать существующий маленький
синтетический набор за достаточный production acceptance test.

Включение pipeline — только после этих gates. Текущая рекомендация: готовить такой
pilot; **не заменять FTS на безусловный semantic top-k** и не объявлять RRF причиной
улучшения, которого сравнение не показало.
