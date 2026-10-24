# Отчёт о реализации и проверке DOCX Template Generator

24 октября 2026. Рабочая копия: D:\Projects\hackathon. Изменения не закоммичены и не отправлены. Основной Docker Compose работает, база и volumes сохранены. Существующие изменения semantic/RAG/инфраструктуры, присутствовавшие до задачи, оставлены на месте.

## Результат

Реализован полноценный backend: регистрация версии DOCX, каталог с ACL, личные постоянные черновики, детерминированные вопросы, типизированные ответы, progress/revision, валидация, идемпотентная генерация через существующий Python engine и защищённое скачивание. Схема и результаты находятся в PostgreSQL; исходные и готовые файлы — в существующем persistent storage. Настоящий модуль рабочей группы прошёл публичный Go API и Python, сохранённый DOCX распакован, прочитан python-docx и открыт LibreOffice.

Архитектура, таблицы/ограничения/индексы, все 14 endpoints, примеры JSON, авторизация, ошибки и команды запуска подробно описаны в [template-generator.md](template-generator.md). Аудит перед реализацией — [template-generator-audit.md](template-generator-audit.md). Ручной профиль — [working-group.json](template-profiles/working-group.json).

## Что добавлено и переиспользовано

Добавлены доменные модели DocumentTemplate, TemplateDraft, GeneratedDocument, TemplateField/Binding/Validation; интерфейсы Templates, TemplateVersions, TemplateEngine; TemplateService и PostgreSQL TemplateRepository. Подключение — через существующие cmd/server и Modules в transport/http. Go использует общий aiclient.Client для существующих `/internal/v1/templates/inspect` и `/render`, передавая multipart template/values/schema и X-Service-Token.

Переиспользованы Documents.GetVersionByID, DocumentAuthorization/DocumentAccessService, DocumentStorage/os.Root, document_versions, users, document_access, session auth, RBAC, CSRF, HTTP transport, zap и Goose. В существующем storage добавлена публикация через временный файл и Rename. В CORS добавлен PATCH. ChatService, RAG, embeddings и frontend для этой задачи не переписывались.

Новая аддитивная миграция 00009 создаёт document_templates, template_drafts и template_generated_files. Миграции 00001–00008 не изменялись этой задачей. Составные FK закрепляют реальные версии/владельцев; UNIQUE(draft_id,draft_revision) и PostgreSQL FOR UPDATE устраняют дубли; CAS revision предотвращает потерю обновлений. Для новой схемы/версии регистрируется новый профиль. Результаты разных revision не перезаписываются.

## Реальный DOCX и значения

Проверен исходный `data/demo/Модуль_распоряжения_о_создании_рабочей_группы.docx`. Это не Jinja-шаблон: линии и номера примечаний находятся в обычных параграфах и таблицах, часть текста разбита на runs. Ручной профиль содержит 14 полей; дополнительны утверждающее лицо, дата утверждения и организация.

| Поле | Синтетическое значение в E2E |
|---|---|
| approval_person | Уполномоченное тестовое лицо |
| approval_date | 2026-10-24 → 24.10.2026 |
| organization | Тестовая организация |
| group_subject | Внедрение электронного архива, в заголовке и п. 1 |
| purpose | Подготовка предложений по внедрению электронного архива документов |
| leader | Иванов И.И., начальник отдела цифровизации |
| secretary | Петрова А.А., специалист отдела ДОУ |
| members | Сидоров С.С., специалист ИТ; Кузнецова Е.В., специалист архивного подразделения |
| deadline | 2026-12-15 → 15.12.2026 |
| actions | Проанализировать текущие процессы хранения документов и подготовить план внедрения |
| result | План мероприятий по внедрению электронного архива на рассмотрение руководства |
| termination | представления плана мероприятий, после исходного слова «после» |
| controller | Смирнов А.В., начальник управления |
| signer | Фамилия, инициалы уполномоченного подписанта, заданные в тесте |

Заполнены таблица утверждения, соответствующие строки состава и текстовая ячейка подписанта. Два члена отображаются отдельными строками в ячейке «Члены». Удалены линии и [1]–[8] в заменённых местах; точки сохранены через suffix в профиле. Раздел инструкций, строка ручной подписи, дата/номер НЕ УДАЛЯТЬ, визуализация ЭП, герб/его место и колонтитулы намеренно сохранены.

Автоматическая проверка сравнила набор ZIP-частей и байты всех частей кроме word/document.xml. Внутри изменённой части сравнены w:pPr, w:rPr, w:tblPr, w:tcPr, w:sectPr. XML валиден; ZIP CRC корректны; оригинал неизменён. LibreOffice успешно преобразовал финальный DOCX в PDF, просмотрены все три страницы PNG. Обрезанного текста/наложений не обнаружено. Строка состава переносится через границу страниц по исходной вёрстке; свойства таблицы не изменялись. Microsoft Word отдельно не запускался.

Финальный локальный образец: `.local-integration/template-e2e/working-group-filled.docx`.

Последний E2E:

```text
template_id: 9d681e0a-6bfa-4cb5-809c-ff4a48f01390
draft_id: cd197df2-c8ef-414d-b4ab-3bcb72f190ce
generated_document_id: 0030c519-7125-4827-9436-cb7c29ebae24
source SHA-256: 5f594e008ae4b47a297637794d0cc2f0b982072bb085f414582c576d793dbab4
result SHA-256: 7dd7d32a923792d66cc89cb345bc77232b5b6a936f7a4d30d8186760f758ab57
```

## Безопасность и ошибки

Проверены auth/CSRF на всех template routes, admin/member ограничения, запрет чтения чужого draft/result даже администратором, неизвестные UUID, disabled actor, отзыв/восстановление document ACL. Прямой URL известного generated UUID после отзыва ACL возвращает 404. Повторная ACL-проверка после Python предотвращает сохранение результата, если право отозвано во время рендеринга.

Проверены неизвестные ключи, неверные типы, пробелы, даты, длинные строки, пустые обязательные списки, person_list, привязка ответа к текущему question key, stale revision, параллельная генерация. Все вводимые реквизиты берутся из пользовательских значений; модель не назначает даты/ФИО/подписантов.

Python проверяет недоверенный ZIP/XML: размеры, число частей, traversal, макросы/embedded objects, внешние relationships, DTD, исполняемые Jinja-конструкции и Word fields кроме пассивных PAGE/NUMPAGES. Значения экранируются, не интерпретируются как код; путь/SQL/stack trace/PII не выдаются клиенту. Go проверяет MIME, размер, непустоту и ZIP/XML ответа, затем настоящий формат в storage. Файлы доступны через защищённый endpoint, случайные физические имена не раскрываются.

При сбое Python нет успешных метаданных; draft и предыдущие результаты сохраняются. Файл удаляется после ошибки INSERT/транзакции с известным откатом. Неопределённый COMMIT возвращает 503 и сохраняет файл для безопасного retry; порядок аварийной сверки файлов описан в документации. Это не распределённая транзакция между PostgreSQL и файловой системой.

## Выполненные проверки

| Проверка | Фактический результат |
|---|---|
| go test ./... с TEST_DATABASE_URL | Все пакеты и интеграционные тесты прошли на отдельной PostgreSQL 17 + pgvector |
| go vet ./... | Прошёл |
| go test -race ./internal/service ./internal/repository/postgres ./internal/aiclient с TEST_DATABASE_URL | Прошёл на Windows, включая реальные repository tests |
| pytest локального venv | 145 passed, 6 skipped: не задан локальный EMBEDDING_TEST_MODEL_PATH |
| Полный pytest в Compose run с существующей E5-моделью | 151 passed, без пропусков |
| Docker build api/ai-service и up | Прошли; основная база получила только новую миграцию 9 |
| E2E A: реальная регистрация → wizard → generate → download | Прошёл, включая ZIP/XML, значения, таблицы, свойства стилей |
| E2E B: неполный draft | 422, недостающие поля, результат не создаётся |
| E2E C: отзыв ACL | Draft/generate/download 404, список скрывает draft, восстановление права возвращает доступ |
| E2E D: рестарты | Частичный draft продолжен после restart, готовый файл скачан с прежними байтами |
| E2E E: остановленный Python | 502, сохранён draft/старый результат, после start успешный новый результат |
| Идемпотентность | Два параллельных generate одной revision вернули одинаковый ID; новая revision получила другой ID |
| LibreOffice | Финальный DOCX открылся, экспортирован в PDF; все три страницы просмотрены |
| Реальный search/RAG smoke | Публичный search вернул 5 источников; chat mode=generated с 5 проверенными ссылками, история сохранена |
| Semantic search | Включён в основной конфигурации; E5 real-model tests прошли, при smoke нет semantic FTS fallback в логах |
| git diff --check | Прошёл |

Первые диагностические запуски выявили два исправленных замечания: запуск pytest из корня без PYTHONPATH и старое ожидание версии миграций 8 в существующем integration test. Итоговые результаты выше относятся к исправленной версии. Во время управляемого отключения Python существующий semantic worker ожидаемо повторял индексацию, после восстановления завершил её.

## Изменённые этой задачей файлы

Новые:

```text
ai-service/tests/test_configured_templates.py
docs/template-generator-audit.md
docs/template-generator.md
docs/template-generator-verification.md
docs/template-profiles/working-group.json
internal/domain/template.go
internal/domain/template_test.go
internal/repository/postgres/template_repository.go
internal/repository/postgres/template_repository_integration_test.go
internal/service/template_service.go
internal/service/template_service_test.go
internal/aiclient/templates.go
internal/aiclient/templates_test.go
internal/transport/http/handlers/templates.go
internal/transport/http/handlers/templates_test.go
internal/transport/http/templates_test.go
migrations/00009_document_templates.sql
scripts/template_e2e.py
scripts/template_regression_smoke.py
```

Изменены существующие:

```text
ai-service/app/api/templates.py
ai-service/app/schemas/templates.py
ai-service/app/templates/engine.py
cmd/server/main.go
docs/templates-contract.md
internal/repository/postgres/documents_integration_test.go
internal/service/document_storage.go
internal/transport/http/middleware/csrf.go
internal/transport/http/router.go
```

Другие видимые git status изменения существовали до этой задачи и не входят в данный список.

## Ограничения и React

Для новых обычных DOCX без placeholders требуется проверенная ручная схема; автоматическое обнаружение произвольных линий не заявляется. Person list выводится строками одной ячейки; сложное динамическое клонирование строк и Jinja-циклы не поддерживаются. Согласование падежей/регистра и юридическая проверка текста остаются за пользователем. Имена переносятся дословно. Неподдерживаемые/опасные Word fields отклоняются, а не уничтожаются.

Полноценной страницы React пока нет, что разрешено заданием. Все необходимые операции для каталога, wizard, review, редактирования, генерации и скачивания уже доступны. Осталось отобразить этот контракт в React/TanStack Query, обрабатывать 409 через перечитывание draft и показывать values перед явной кнопкой generate.

Все backend acceptance criteria проверены. Поддержка Microsoft Word подтверждена структурой DOCX и LibreOffice, но непосредственный запуск Microsoft Word не выполнялся. Абсолютное совпадение переносов строк между Word и LibreOffice не заявляется. Основной stack оставлен работающим; тестовые записи E2E оставлены для просмотра. Временная отдельная PostgreSQL для Go tests и QA-контейнер LibreOffice удаляются после завершения проверки.
