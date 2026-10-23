# Search API: PostgreSQL Russian FTS

Сохраняемые диалоги поверх этого поиска описаны в [Chat API](chat.md).

Поиск возвращает сохранённые фрагменты и точные метаданные источников. Он не
генерирует ответ, не вызывает Python или LLM и не требует работающего ingestion
worker. Для поиска нужны уже сохранённые chunks активной версии со статусом ready.

## HTTP-контракт

```http
POST /api/v1/search
Content-Type: application/json
Cookie: ai_session=<session cookie>
Origin: <configured trusted origin>
X-CSRF-Token: <session CSRF token>
```

```json
{
  "query": "Как согласовать служебную записку?",
  "limit": 10
}
```

Пример формы ответа (данные иллюстративные):

```json
{
  "query": "Как согласовать служебную записку?",
  "mode": "search",
  "results": [
    {
      "chunk_id": "50863de6-a38f-4715-acf6-57e140ec663f",
      "document_id": "8d2f02ae-405d-43af-bd04-694f7bb21143",
      "document_version_id": "dc5e665c-ef5c-4304-9389-32cdc2395804",
      "document_title": "Инструкция по документообороту",
      "version_number": 2,
      "chunk_index": 3,
      "content": "Текст сохранённого пункта инструкции...",
      "page_number": 12,
      "section_number": "2.3.1",
      "section_title": "Согласование документов",
      "score": 0.42
    }
  ],
  "total": 1
}
```

`mode` всегда `search`. `score` — исходное значение `ts_rank_cd`, внутренний ранг
релевантности, без округления до «процента достоверности». Номер страницы, раздел
и его заголовок берутся из chunks без дополнения или генерации; неизвестные
значения возвращаются как null. IDs и номер версии соответствуют существующей
активной ready-версии документа. Для открытия точного оригинала используйте
`GET /api/v1/documents/{document_id}/versions/{document_version_id}/download`:
endpoint повторно применяет существующие auth/ACL.

При отсутствии совпадений:

```json
{"query":"несуществующееслово","mode":"search","results":[],"total":0}
```

`total` равен **длине results после LIMIT**, а не общему количеству совпадений в
БД. Если совпало 50 разрешённых чанков и limit=10, ответ содержит total=10.
Отдельного COUNT, пагинации и сведений о запрещённых документах нет.

| Параметр | Правило |
| --- | --- |
| query | Строка UTF-8, непустая после TrimSpace, без NUL |
| Длина query | Максимум 500 Unicode-символов, включая окружающие пробелы до нормализации |
| limit | По умолчанию 10; отсутствующее/null/0 значение выбирает default; допустимы 1..30 |
| JSON | Только query и limit; неизвестные поля и лишние JSON-значения запрещены |
| Тело запроса | Максимум 4096 байт, общий JSON helper существующего API |
| Запрос в БД | Не более 5 секунд; меньший deadline/cancellation вызывающего контекста сохраняется |

В ответе query очищен от окружающих пробелов. Пунктуация и стоп-слова могут дать
пустой tsquery: это нормальный результат 200 с пустым results. Поиск лексический:
он не переформулирует вопрос и не гарантирует понимание произвольного вопроса
естественным языком. `websearch_to_tsquery` поддерживает кавычки для фразы, `OR`
для альтернатив и минус для исключения; ACL действует на все варианты выражения.

Статусы используют существующий JSON-формат `{"error":"..."}`:

| Статус | Значение |
| --- | --- |
| 200 | Успешный поиск, включая отсутствие совпадений |
| 400 | Невалидные JSON, Content-Type, query или limit |
| 401 | Нет действующей сессии; пользователь неактивен |
| 403 | Неверные/отсутствующие Origin или CSRF |
| 500 | Ошибка БД, таймаут или другая внутренняя ошибка; детали SQL скрыты |

Чужие документы и отсутствие совпадений не различаются по структуре ответа.
Поиск доступен admin и member; роль и user_id не принимаются из тела.

## SQL и ACL

Схема и индекс миграций 00003–00005 достаточны. Новые миграции/индексы не нужны:
существующий `document_chunks_content_fts_idx` индексирует именно
`to_tsvector('russian'::regconfig, content)`. Это же выражение использовано в
предикате поиска. PostgreSQL выбирает план с учётом объёма данных и селективности;
на маленькой таблице последовательное сканирование тоже может быть оптимальным.

Упрощённая форма реального параметризованного запроса:

```sql
WITH search_query AS (
    SELECT websearch_to_tsquery('russian'::regconfig, $3) AS query
)
SELECT c.id, d.id, v.id, d.title, v.version_number, c.chunk_index,
       c.content, c.page_number, c.section_number, c.section_title,
       ts_rank_cd(to_tsvector('russian'::regconfig, c.content), q.query) AS score
FROM document_chunks c
JOIN document_versions v ON v.id = c.document_version_id
JOIN documents d ON d.id = v.document_id AND d.active_version_id = v.id
JOIN users u ON u.id = $1 AND u.is_active AND u.role = $2
CROSS JOIN search_query q
WHERE v.status = 'ready'
  AND (u.role = 'admin' OR EXISTS (
      SELECT 1 FROM document_access a
      WHERE a.document_id = d.id AND a.user_id = u.id
  ))
  AND to_tsvector('russian'::regconfig, c.content) @@ q.query
ORDER BY score DESC, d.id ASC, v.id ASC, c.chunk_index ASC, c.id ASC
LIMIT $4;
```

Параметры: доверенные user ID, роль, текст запроса и limit. Ввод пользователя не
конкатенируется с SQL. Фильтрация ACL, active/ready и поиск выполняются в одном
statement/snapshot до возврата данных в Go. Member должен иметь строку в
document_access; admin не требует ACL. Состояние пользователя и совпадение роли
дополнительно проверяются в БД, чтобы устаревший контекст не расширил доступ после
деактивации/смены роли. Отозванный ACL исключается из следующего SQL-запроса после
commit; уже выполняющийся запрос использует свой снимок PostgreSQL. Кэша прав или
результатов нет.

Неактивные/неизвестные версии, historical ready и ещё не опубликованные ready
не участвуют в выдаче. Изменение активного указателя сразу меняет выбор версии
для следующих поисков. Ошибка чтения БД не возвращает частичную выборку.

## Слои и будущее использование

- Domain содержит SearchQuery с нормализацией/ограничениями и SearchResult —
  проекцию цитаты с идентификаторами чанка, документа и версии. DocumentChunk
  остаётся моделью хранения; в SearchResult нет служебных timestamps/storage key.
- SearchService проверяет активного actor, ID, роль, текст и limit, затем вызывает
  потребительский интерфейс SearchStore, объявленный рядом с сервисом.
- PostgreSQL SearchRepository выполняет один ограниченный по времени запрос и
  возвращает только разрешённые источники.
- HTTP handler берёт actor из существующего auth context и использует общие JSON
  helpers, RequireAuth и OriginPolicy.CSRF.

Будущий ChatService получает тот же `*service.SearchService` через DI и вызывает
его напрямую, без HTTP:

```go
sources, err := search.Search(ctx, authenticatedUser, domain.SearchQuery{
    Query: question,
    Limit: 10,
})
```

`authenticatedUser` должен поступать из доверенной аутентификации. Передавать
модели можно только успешно полученные источники; нельзя повторно получать все
чанки в обход ACL или переиспользовать результаты другого пользователя. LLM,
ChatService и универсальный retrieval engine в этом изменении не реализованы.

## Безопасность текста

POST не помещает запрос в URL. Go не логирует query, body или содержимое чанков.
Ответы имеют JSON Content-Type, `Cache-Control: no-store` и nosniff. Контент
возвращается как текст, без HTML-подсветки. JSON encoder экранирует HTML-символы,
но после разбора JSON исходный текст всё равно недоверенный: клиент должен
использовать безопасный текстовый вывод, без innerHTML/dangerouslySetInnerHTML.

## Проверки

```powershell
go mod tidy
go test ./...
go vet ./...
go build ./cmd/server
$env:TEST_DATABASE_URL = 'postgres://test:password@127.0.0.1:5432/search_test?sslmode=disable'
go test ./... -count=1
go test -race ./internal/service ./internal/transport/http/... ./internal/repository/postgres ./tests/integration
```

Интеграционные тесты используют только отдельную TEST_DATABASE_URL с суффиксом
`_test` и изолированные схемы. Подготовленные chunks вставляются через PostgreSQL
repository; Python и LLM не нужны. Проверяются ACL, grant/revoke, неактивные
пользователи, stale admin role, активные/исторические версии, все неготовые статусы,
публикация, русская морфология, ranking/tie order, точность цитат, nullable metadata,
лимиты, SQL injection, пустые результаты, cancellation и сквозная HTTP-защита.
