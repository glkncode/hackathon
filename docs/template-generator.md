# Генерация DOCX по шаблону

Актуальное расширение: [Template Generator 2.0](template-generator-v2.md), [аудит источников](template-generator-v2-audit.md), [проверки и результаты](template-generator-v2-verification.md). Разделы ниже описывают сохраняемый контракт v1. Нормативная очистка, условные вопросы, реальные строки таблиц и отчёты включаются через явный профиль v2.

Backend поддерживает полный сценарий: регистрация загруженной версии DOCX → личный черновик → вопросы → проверка → генерация → защищённое скачивание. Пользовательский [React Wizard](template-wizard.md) доступен в разделе «Шаблоны» через существующий Nginx на http://localhost:8080. Результаты UI-проверок: [template-wizard-verification.md](template-wizard-verification.md).

## Архитектура и версии

`TemplateService` получает через DI `Templates`, `TemplateVersions`, `DocumentAuthorization`, `DocumentStorage`, `TemplateEngine` и zap logger. PostgreSQL-репозиторий реализует хранение всех трёх сущностей. Общий `aiclient.Client` передаёт исходный DOCX, schema и values multipart-запросом в существующий Python FastAPI, с X-Service-Token, существующим transport, запретом redirects/proxy, context cancellation и таймаутом до 60 секунд. Qwen не участвует в заполнении.

Одна запись document_templates — неизменяемая регистрация схемы для конкретной document_version_id. Можно менять только enabled. Для новой версии документа или другой схемы регистрируется новая запись. Старые черновики не меняют template_id, document_version_id и схему. template_id служит идентификатором опубликованного профиля, document_version_id — идентификатором исходного DOCX, schema_version сейчас 1.

Миграция `00009_document_templates.sql` добавляет document_templates, template_drafts, template_generated_files. UUID PK, составные FK обеспечивают принадлежность версии исходному документу, черновика шаблону, результата владельцу и версии черновика. JSONB хранит только схему/значения; бинарный DOCX остаётся в существующем storage. CHECK ограничивают статусы, revision, размер JSON, MIME, размер результата, SHA-256. Индексы обслуживают каталог, owner/template/draft и source-version; UNIQUE(draft_id,draft_revision) обеспечивает идемпотентность.

Все PATCH используют CAS: UPDATE ... WHERE revision=expected_revision, затем revision+1. При конфликте 409. Генерация держит FOR UPDATE на одной строке черновика, проверяет revision, возвращает предыдущий результат той же revision либо рендерит и вставляет новый. Параллельные изменения ждут блокировку, затем конфликтуют по revision; глобальных mutex нет. Новая revision даёт самостоятельный generated_document_id. Изменение даже на то же значение увеличивает revision.

Хранилище пишет в закрытый случайный `.UUID.tmp`, проверяет DOCX, делает Sync/Close и атомарный Rename в UUID. Только после этого создаются метаданные результата. При ошибке сохранения/INSERT файл удаляется. Если подтверждение COMMIT потеряно, возвращается 503 и файл сохраняется: повтор той же revision найдёт подтверждённый результат, если транзакция была зафиксирована. Нельзя удалять файл только на основании ошибки COMMIT.

После аварийного завершения процесса между Rename и INSERT возможен приватный файл без метаданных. При обслуживании с остановленным API такие файлы можно сверить с объединением `document_versions.storage_key` и `template_generated_files.storage_key`; удалять следует только неподтверждённые файлы после проверки отсутствия обеих ссылок и с запасом по времени. Рабочие volumes не очищаются автоматически. Ошибки обычных запросов покрыты автоматической очисткой.

## Авторизация

Для всех операций нужна действующая session cookie. Для POST/PATCH нужны доверенный Origin и X-CSRF-Token из существующего CSRF cookie. CORS разрешает PATCH. Только admin регистрирует/отключает шаблоны. Member использует разрешённые исходные документы.

Черновик и результат доступны только owner_id, включая запросы администратора. Доступ к исходному документу проверяется заново для чтения, обновления, проверки, генерации и скачивания, включая уже созданные файлы. Каталоги фильтруются по актуальным users.is_active и document_access до pagination. Отзыв ACL и enabled=false скрывают связанные ресурсы; недоступный UUID возвращает 404. Восстановление ACL возвращает доступ. Роли, сессии, cookies и CSRF переиспользуются без отдельной системы пользователей.

Storage key/пути не попадают в JSON. Скачивание идёт только через Go после ownership/ACL, с attachment, nosniff, no-store и sandbox CSP. os.Root и UUID предотвращают traversal и symlink escape. DOCX не направляется в LLM или сторонний сервис, содержимое не исполняется.

## Маршруты

Все пути начинаются с `/api/v1`.

| Метод | Путь | Результат |
|---|---|---|
| POST | /templates | admin: зарегистрировать профиль, 201 |
| GET | /templates?limit=20&offset=0 | доступные шаблоны |
| GET | /templates/{id} | описание, fields, версия |
| PATCH | /templates/{id} | admin: `{ "enabled": false }` |
| POST | /templates/{id}/drafts | пустой личный черновик, 201 |
| GET | /template-drafts?limit=20&offset=0 | свои доступные черновики |
| GET | /template-drafts/{id} | значения, fields, revision, прогресс |
| PATCH | /template-drafts/{id} | редактирование values с expected_revision |
| GET | /template-drafts/{id}/next-question | готовность и первый обязательный вопрос |
| POST | /template-drafts/{id}/answers | ответ на текущий вопрос |
| POST | /template-drafts/{id}/validate | проверка типов и заполненности |
| POST | /template-drafts/{id}/generate | результат текущей expected_revision |
| GET | /generated-documents/{id} | метаданные без storage key |
| GET | /generated-documents/{id}/download | настоящий DOCX |

Списки возвращают `{ "items": [], "limit": 20, "offset": 0 }`. limit 1–100; offset до 1 000 000. Создание черновика и validate принимают пустое тело. Остальные изменяющие операции принимают application/json, неизвестные свойства отклоняются.

### Регистрация и схема

Предварительно загрузите DOCX через существующий POST /documents multipart с title и file. Версию возьмите из latest_version.id либо явно выберите из истории.

```json
{
  "document_id": "<document UUID>",
  "document_version_id": "<version UUID>",
  "name": "Распоряжение о создании рабочей группы",
  "description": "Распоряжение с составом и сроком",
  "fields": [
    {
      "key": "recipient",
      "label": "Адресат",
      "type": "person",
      "required": true,
      "question": "Кому адресован документ?",
      "description": "Укажите должность, инициалы и фамилию",
      "max_length": 2000
    }
  ]
}
```

Без fields Python автоматически обнаружит простые `{{ recipient }}`. Go создаст строковые обязательные поля и вопросы из их имён. Для удобных подписей/типов можно передать полный набор метаданных; Python проверит точное совпадение ключей с placeholders. Непустой DOCX без распознаваемых полей и без ручной схемы не регистрируется.

Для обычных линий используется явная привязка в fields[].bindings:

```json
{
  "part": "word/document.xml",
  "paragraph": 19,
  "match": "____________________________________________",
  "suffix": ""
}
```

paragraph — нулевой индекс среди всех w:p данной ZIP-части, включая таблицы. match обязан встретиться в выбранном параграфе ровно один раз. Привязки не могут пересекаться или изменять параграф с НЕ УДАЛЯТЬ. suffix — необязательный буквальный текст до 128 символов, например точка при замене заполнителя вместе с номером примечания. Один field может иметь до 16 привязок. Все поля либо используют явные bindings, либо являются обычными placeholders; смешанная схема отклоняется. Допустимы document, headerN, footerN и footnotes XML. Ни произвольных путей, ни XPath, ни исполняемого кода в профиле нет.

Типы: text, multiline_text, person, date, person_list. person — введённая пользователем строка, без выдумывания/разбора ФИО; person_list — массив непустых строк, до 50 участников, отображается отдельными строками в ячейке/поле. Дата сохраняется строго YYYY-MM-DD и рендерится DD.MM.YYYY. Пустые значения, null и пробелы не закрывают обязательное поле. PATCH разрешает очистить поле; answers требует непустой валидный ответ на текущий вопрос. Неизвестные ключи и неверные типы не сохраняются. Необязательные поля заполняются через PATCH.

### Черновик и ответ

```json
{
  "id": "<draft UUID>",
  "template_id": "<template UUID>",
  "document_version_id": "<pinned version UUID>",
  "owner_id": "<user UUID>",
  "values": {},
  "status": "draft",
  "revision": 1,
  "schema_version": 1,
  "required_field_count": 14,
  "completed_field_count": 0,
  "fields": []
}
```

Ответ на текущий вопрос:

```json
{"field_key":"leader","value":"Иванов И.И., начальник отдела цифровизации","expected_revision":5}
```

Редактирование нескольких известных полей:

```json
{"expected_revision":6,"values":{"deadline":"2026-12-15","members":["Сидоров С.С., специалист ИТ","Кузнецова Е.В., специалист архивного подразделения"]}}
```

Сохранение возвращает полный обновлённый draft с новой revision. Следующий вопрос/validate:

```json
{
  "ready":false,
  "missing_fields":[{"key":"leader","label":"Руководитель","type":"person","required":true,"question":"Кого назначить руководителем?","description":"","max_length":2000}],
  "invalid_fields":[],
  "next_question":{"key":"leader","label":"Руководитель","type":"person","required":true,"question":"Кого назначить руководителем?","description":"","max_length":2000}
}
```

В полном черновике status=ready_to_generate, ready=true, missing_fields=[], invalid_fields=[], next_question=null. Go выполняет детерминированную проверку, Python дополнительно проверяет binding/schema/DOCX при регистрации и рендеринге. Никакие реквизиты не дополняются моделью.

### Генерация

```json
{"expected_revision":15}
```

Успех 200, включая повтор той же revision:

```json
{
  "generated_document_id":"<generated UUID>",
  "draft_id":"<draft UUID>",
  "draft_revision":15,
  "status":"ready",
  "filename":"generated-document.docx",
  "download_url":"/api/v1/generated-documents/<generated UUID>/download"
}
```

Необходимость review перед generate обеспечивает будущий frontend: покажите сохранённые values и дайте явную кнопку генерации. Backend никогда автоматически не генерирует документ после последнего ответа.

| HTTP | Значение |
|---|---|
| 400 | неверный JSON, поле, тип, дата, профиль или ответ |
| 401 | нет действующей сессии, в том числе disabled user через auth |
| 403 | CSRF/Origin/RBAC |
| 404 | нет ресурса, чужой owner, отозванный ACL, отключённый шаблон |
| 409 | устаревшая revision или ответ на другой вопрос |
| 413 | лимит JSON/исходного DOCX/schema/values |
| 422 | неполный/некорректный черновик при generate; тело TemplateValidation |
| 502 | Python недоступен, timeout, неверный/пустой/слишком большой результат |
| 503 | заняты четыре Go-слота либо неопределённый COMMIT, повторите ту же revision |

Обычная ошибка — `{ "error": "safe message" }`; 422 содержит структурированный результат проверки. Stack traces, SQL details, значения/токены не логируются.

## Реальный модуль рабочей группы

Профиль `docs/template-profiles/working-group.json` предназначен для неизменённого DOCX из data/demo с рабочей группой. Он содержит 14 полей: approval_person, approval_date, organization, group_subject, purpose, leader, secretary, members, deadline, actions, result, termination, controller, signer. group_subject заполняется в двух местах. Участники остаются в строке «Члены» таблицы состава, каждый с новой строки. Примечания [1]–[8] удаляются только в заполненных местах, поясняющий раздел примечаний остаётся. Точки после result/termination/controller задаются suffix.

Сохраняются исходные paragraph/run/table/section properties, все нетронутые ZIP parts, header/footer/PAGE, герб/его место, дата/регистрационный номер НЕ УДАЛЯТЬ, строка ручной подписи и визуализация ЭП. Электронная подпись, регистрационный номер и текущая дата не создаются. Дата утверждения вводится отдельно. Поля PAGE/NUMPAGES разрешены как пассивное содержимое; другие Word instructions, макросы, embedded objects, внешние relationships, DTD/entities, ZIP traversal, исполняемый Jinja отклоняются. Никакие внешние ссылки не выполняются.

Текст переносится дословно. Склонение, регистр, грамматику и юридическую корректность проверяет пользователь. С длинным составом Word/LibreOffice может перенести часть строки таблицы на следующую страницу согласно исходным свойствам. Движок сохраняет свойства шаблона, не меняет его вёрстку ради фиксированного числа страниц. Для другого обычного DOCX нужен проверенный профиль привязок; автоматического распознавания произвольных линий нет.

Лимиты: источник 5 МиБ, результат 10 МиБ, распакованный DOCX 32 МиБ/1000 частей, до 64 полей, max_length до 8000 Unicode-символов (в рабочем профиле 2000), JSON schema/values до 64 КиБ каждый, HTTP JSON до 256 КиБ, список до 100 результатов, до 50 участников, Go generation concurrency 4, Python template concurrency 2, generation deadline 60 секунд. Очередь/Redis не добавлялись.

## Запуск и воспроизводимые проверки

```powershell
docker compose up -d --build api ai-service
go test ./...
go vet ./...
Push-Location ai-service
.venv/Scripts/python.exe -m pytest tests -q -rs -p no:cacheprovider
Pop-Location
ai-service/.venv/Scripts/python.exe scripts/template_e2e.py --restart
ai-service/.venv/Scripts/python.exe scripts/template_regression_smoke.py
```

E2E использует реальные PostgreSQL, Go API, Python и файлы. Создаёт два синтетических member, документ и шаблон с префиксом E2E; записи остаются для проверки. Секреты берёт из локального .env, не печатает. --restart кратковременно перезапускает api/ai-service и останавливает Python для сценария отказа; finally восстанавливает Python. Без флага проверяются сценарии без перезапуска. Артефакты — в игнорируемой `.local-integration/template-e2e`, без cookies/паролей.

Для интеграционных Go-тестов задайте отдельную PostgreSQL 17 + pgvector БД с именем, заканчивающимся `_test`. Не используйте основную DATABASE_URL. Например, отдельный контейнер без production volumes:

```powershell
docker run --rm --name hackathon-template-tests -e POSTGRES_PASSWORD=template_test_password -e POSTGRES_DB=templates_test -p 127.0.0.1:55439:5432 -d pgvector/pgvector:pg17-bookworm
$env:TEST_DATABASE_URL='postgres://postgres:template_test_password@127.0.0.1:55439/templates_test?sslmode=disable'
go test ./...
go test -race ./internal/service ./internal/repository/postgres ./internal/aiclient
docker stop hackathon-template-tests
```

Существующая тестовая инфраструктура создаёт/удаляет только собственные схемы в этой тестовой БД. Production volumes, старые миграции, RAG, semantic search и frontend не переписываются.

Полный pytest с реальной уже установленной E5-моделью, без скачивания весов:

```powershell
docker compose run --rm --no-deps -e EMBEDDING_TEST_MODEL_PATH=/models/e5-small -v 'D:/Projects/hackathon:/qa:ro' -w /qa/ai-service --entrypoint python ai-service -m pytest tests -q -rs -p no:cacheprovider
```

Путь mount в этой команде заменяется на путь вашей рабочей копии. Production Dockerfiles и Compose не требуют дополнительных пакетов для тестов или визуального QA. `template_regression_smoke.py` вызывает публичные search/chat API и настроенный разрешённый inference-сервер; временную чат-сессию архивирует после проверки.
