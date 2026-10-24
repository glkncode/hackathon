# React Template Wizard 2.0

Локальное приложение: http://localhost:8080. Frontend использует существующий Go API, session cookie и CSRF. Генерация проходит через Go → Python; черновики, revision и immutable snapshots хранятся в PostgreSQL. DOCX хранится в закрытом persistent storage. Браузер не обращается к Python и не создаёт файлы самостоятельно.

## Страницы и компоненты

| URL / файл | Назначение |
|---|---|
| `/templates` — `TemplatesPage` | Доступный каталог, пагинация, мои черновики, серверный прогресс |
| `/templates/drafts/:id` — `TemplateDraftPage` | Последовательные вопросы, сохранение, конфликт revision, review, валидация, генерация |
| `/templates/results/:id` — `TemplateResultPage` | Постоянная ссылка на результат, метаданные, отчёт snapshot, повторное скачивание |
| `/templates/register` — `TemplateRegisterPage` | Admin: исходный документ, закреплённая версия, загрузка подготовленного JSON-профиля v2 |
| `TemplateFieldRenderer` | text, multiline_text, date, person, person_list, text_list, choice, repeater |
| `ValidationReport`, `SourceNote`, `ApprovalRoute` | Категории проверок, источники, рекомендуемый маршрут визирования |
| `useTemplates`, `api/templates`, `types/templates` | Query conventions, DTO и фактический HTTP-контракт |

Все страницы включены в существующие ProtectedRoute/AppLayout; раздел «Шаблоны» находится в общей боковой навигации. Используются существующие Feedback, кнопки и стили продукта. Формы имеют подписи, сообщения ошибок, native date picker и управление с клавиатуры; новый вопрос получает фокус заголовка. Списки участников сохраняются массивами объектов, позволяют добавлять, удалять и менять порядок строк. В v1 person_list остаётся массивом строк.

## Аудит и фактический контракт

Проверены `frontend/src/app/{router,providers}.tsx`, auth API/hooks, API client, общий download helper, layout, документы и тестовые conventions. Контракт сверялся с `internal/transport/http/{router.go,handlers/templates.go}`, `internal/domain/template*.go`, `internal/service/template_service.go`, PostgreSQL repository, `ai-service/app/templates/profile.py`, семью JSON-профилями v2 и scripts/template*_e2e.py. Go/Python и миграции 00001–00011 этой UI-задачей не менялись.

Различия между кратким описанием задачи и фактическим API:

- Самостоятельного типа boolean нет: предусмотрен `choice` со строками `yes/no` и иногда `unknown`. UI переводит подписи и отправляет исходные значения.
- `person` — строка с предусмотренной профилем грамматической формой; `repeater` — массив объектов с точными item_fields. Автоматического кадрового справочника нет.
- V2 GET/update черновика уже возвращает validation для этой revision. Повторный `/next-question` после такого ответа не вызывается: у него нет revision, и он мог бы относиться к более позднему состоянию. V1 использует отдельный `/next-question` с повторной проверкой revision через GET.
- `needs_configuration` фактически возвращается при неудачной регистрации (HTTP 400); текущий каталог содержит только успешно зарегистрированные доступные шаблоны. UI умеет безопасно показать этот статус и на карточке, если API начнёт его возвращать.
- У API нет списка «доступных готовых профилей» и нет отдельного юридического workflow подтверждений. Admin загружает заранее подготовленный JSON; обычный пользователь не редактирует JSON. Ответы на предусмотренные профилем вопросы подтверждения сохраняются через тот же PATCH, без имитации статуса согласования.
- Отдельного списка готовых файлов для черновика нет. Результат открывается по постоянной ссылке. Повтор generate для той же revision возвращает тот же ID.

## Используемые endpoints

Все пути имеют префикс `/api/v1`. POST/PATCH проходят через общий клиент с `X-CSRF-Token`, cookie authentication, `cache: no-store` и AbortSignal для чтений.

| Метод | Путь | UI |
|---|---|---|
| GET | `/templates?limit=20&offset=…` | Каталог с серверным ACL |
| GET | `/templates/{id}` | Название и описание исходного шаблона |
| POST | `/templates/{id}/drafts` | Создать личный черновик |
| GET | `/template-drafts?limit=20&offset=…` | Мои черновики |
| GET | `/template-drafts/{id}` | Ответы, поля, revision, прогресс, v2 validation |
| GET | `/template-drafts/{id}/next-question` | Отчёт и вопрос для legacy v1 |
| POST | `/template-drafts/{id}/answers` | Ответ на текущий серверный вопрос |
| PATCH | `/template-drafts/{id}` | Изменить сохранённое поле, включая необязательное |
| POST | `/template-drafts/{id}/validate` | Проверка на review и непосредственно перед generate |
| POST | `/template-drafts/{id}/generate` | Идемпотентная генерация `{expected_revision}` |
| GET | `/generated-documents/{id}` | Сохранённые метаданные и generation_snapshot |
| GET | `/generated-documents/{id}/download` | Защищённый DOCX |
| POST | `/templates` | Admin: document_id, document_version_id, name, description, profile |

PATCH `/templates/{id}` (enabled) существует в backend, но не требуется минимальному сценарию регистрации и не вызывается этим UI. Login/logout/me, documents и chat используют прежние endpoints.

## Условия, прогресс и сохранение

UI показывает один `validation.next_question`. Review фильтруется по `visible_fields`; обязательность берётся из `required_fields`, для v1 — из field.required. Условия visible_if/required_if не вычисляются в React. Скрытые значения не удаляются: сервер решает, какие сохранённые ответы применять при render. В generate передаётся только revision.

Прогресс использует серверные `required_field_count` и `completed_field_count`; число применимых вопросов может меняться. 100% заполненных полей не отменяет семантических блокирующих правил: generate отдельно требует ready и отсутствие blocking/failed.

POST answers / PATCH отправляют `expected_revision`. После подтверждённой записи Query cache заменяется полным ответом сервера; список черновиков инвалидируется. Для v1 следующий вопрос загружается отдельно, и ошибка этого чтения не выдаётся за ошибку уже сохранённого ответа. Нет основного хранилища ответов в localStorage.

При HTTP 409 локальный ввод остаётся в редакторе. Кнопка сохранения блокируется до явного получения актуального черновика; пользователь видит сохранённый ответ и сам повторяет PATCH с новой revision. Редактор сохраняет свой field key, даже если актуальный next_question уже изменился. Автоматический focus/reconnect refetch для открытого редактора отключён, чтобы не размонтировать несохранённый вопрос. При новом открытии страницы всегда загружается серверное состояние.

Сетевые ошибки и 400 не очищают ввод. Ошибки формата показываются рядом с полем. При 401/403/404 приватный редактор скрывается. При logout общий clearSession удаляет приватный кеш; поздний ответ mutation проверяет актуального owner и не восстанавливает данные вышедшего пользователя. Создание, сохранение, регистрация и генерация защищены от повторных кликов.

## Review, отчёт и файл

Review показывает только применимые реквизиты и сохранённые значения с действием «Изменить». Blocking/failed запрещает generate и содержит переходы к полям: ссылки полей читаются из декларативной структуры соответствующего правила, без исполнения его условий. Успешные/неприменимые автоматические проверки доступны в раскрывающемся списке. Warning, manual_review и info остаются видимыми, вместе с backend source.name/version/section. Manual review никогда не объявляется автоматически пройденной проверкой.

ApprovalRoute отображает required_departments, conditional_departments, applicable, additional_departments и строки Матрицы. Участие ИТ в рабочей группе выводится из реального отчёта; отдельного правила по template_kind в React нет. Статус «Согласовано» не создаётся. «Нет» не считается запретом дополнительного визирования.

Generate сначала вызывает validate, затем существующий endpoint с текущей revision. Идемпотентность обеспечена backend UNIQUE(draft_id,draft_revision), а клиент сохраняет revision для повторов. Результат показывает настоящий ID, имя, дату, профиль и revision. Отчёт берётся из immutable generation_snapshot, а не из позднее изменённого черновика.

Download использует тот же Go origin/cookies. До Blob проверяются HTTP 200, DOCX MIME и Content-Disposition; JSON/206/401/403/404 не сохраняются как документы. Общий helper разбирает UTF-8 filename*, обезвреживает разделители путей, удаляет временную ссылку и освобождает object URL. Storage path не показывается. Ссылку результата можно открыть после обновления страницы или restart контейнеров.

## Проверка и повторение

В frontend: `npm run typecheck`, `npm test`, `npm run lint`, `npm run build`. Если npm отсутствует в PATH, доступны `node node_modules/typescript/bin/tsc -b --pretty false`, `node node_modules/vitest/vitest.mjs run`, `node node_modules/eslint/bin/eslint.js .`, `node node_modules/vite/bin/vite.js build`.

Тестовые данные создаются отдельно: `ai-service/.venv/Scripts/python.exe scripts/template_wizard_e2e.py setup`, с TEMPLATE_WIZARD_TEST_PASSWORD в окружении. Нужны работающий Compose и ранее проверенные помеченные источники из `.local-integration/template-v2-e2e/verification.json`. Скрипт не заполняет браузер за пользователя; browser walkthrough выполняется отдельно через UI.

`verify-browser` сверяет реально скачанные браузером файлы с серверными SHA, snapshot, исходным DOCX и нумерацией. `security-restart` проверяет ownership, CSRF, ACL revoke/restore, идемпотентность и restart stateless api/ai-service/frontend, затем продолжает и завершает тот же частичный черновик. Режимы browser-revoke/browser-restore предназначены только для помеченного тестового working-group source и позволяют проверить соответствующее состояние в UI. После проверки ACL всегда восстанавливается.

Не запускайте много независимых login-сценариев подряд: штатный Go limiter допускает 10 попыток с IP за 15 минут. Он не отключается для E2E. Команды клиентов создают только необходимые сессии. Тестовые артефакты и синтетические записи сохраняются для просмотра, volumes не удаляются.

Фактические результаты: [template-wizard-verification.md](template-wizard-verification.md).
