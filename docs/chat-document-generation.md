# Создание DOCX через AI-чат

Проверено 25 октября 2026 года. Реализован разговорный интерфейс к существующему Template Generator v2: настоящий draft, серверная validation, подтверждение и скачивание настоящего DOCX. Wizard и чат работают с одними данными и renderer. Флаг `CHAT_DOCUMENT_CREATION_ENABLED` по умолчанию **false**; в основном окружении функция не включалась.

Это результат интеграционной и браузерной проверки, а не заявление о безошибочности Qwen. Сбои модели и ограничения перечислены ниже. Commit, push и deploy не выполнялись.

## Аудит и повторное использование

Изучены фактические `ChatService`, `ChatRepository`, HTTP handlers/router, AIClient, FastAPI routes, LLMClient, TemplateService/Repository, миграции, профильная validation, approval routing, DOCX renderer, React Chat и Template Wizard.

Исходный процесс:

```text
ListTemplates/GetTemplate → CreateDraft → UpdateDraftFields/SubmitAnswer
  → GetDraft/ValidateDraft → GenerateDocument → authenticated download
```

Оба интерфейса используют `internal/service/template_service.go`. Новый workflow вызывает существующие `ListTemplates`, `GetTemplate`, `CreateDraft`, `GetDraft`, `UpdateDraftFields`, `GenerateDocument`, `GetGeneratedDocument`. Получение draft v2 уже вызывает `InspectProfile` через существующий Python template engine; отсюда берутся активные/обязательные поля, `next_question`, предупреждения и рекомендации согласования.

Не созданы вторые профили, таблица соответствия фраз шаблонам или renderer. `ai-service/app/templates/profile.py`, `renderer_v2.py` и бизнес-правила профилей не менялись этой задачей. Существующие организаторские DOCX используются как входные файлы; сами исходники не изменялись.

## Workflow и хранение

`chat_document_workflows` содержит session/owner, ссылку на настоящий draft, стадию, version, reviewed_revision, ожидаемое поле редактирования, кандидатов и generated ID. Значения существуют только в `template_drafts.values`.

Стадии: `selecting_template`, `collecting_fields`, `reviewing`, `completed`, `paused`, `cancelled`. Отмена и начало нового workflow не удаляют прежний draft. Его можно открыть в разделе «Шаблоны». Reload и restart API восстанавливают состояние из PostgreSQL.

LLM вызывается перед транзакцией, один раз на естественное сообщение, с пределом 45 секунд. `ChatDocumentRepository.Atomic` блокирует строку принадлежащей пользователю chat session. Сохранение draft, workflow и пары сообщений проходит в одной транзакции; TemplateRepository и ChatRepository получают общий executor через context. CAS редакции draft дополнительно защищает изменения из Wizard. При неуспешном сохранении ни draft, ни workflow, ни сообщения не выходят из rollback.

Генерация сохраняет immutable artifact отдельной существующей транзакцией TemplateRepository, уникально по `(draft_id, draft_revision)`. Если внешний chat turn не сохранился после генерации, повторное действие получает тот же artifact, затем дописывает workflow и сообщения. Успешный ответ выдаётся после сохранения chat turn. Генерация не выполняется внутри транзакции обновления значений draft.

## Intent, шаблон и извлечение

Используется тот же `LLMClient` и настроенная Qwen. Endpoint/модель не заменены, Context Resolver не требуется. RAG system prompt и Evidence Gate этой задачей не менялись.

В Python добавлен приватный `/internal/v1/chat-documents/interpret`, защищённый существующим service token middleware. Go передаёт только доступный ACL-каталог, активную схему полей, текущие значения и ожидаемый вопрос. Каталог ограничен 200 зарегистрированными шаблонами; профили с диска в него не подмешиваются.

Намерения: `knowledge_question`, `create_document`, `template_catalog`, `answer_field`, `edit_document_field`, `continue_document`, `show_document_progress`, `validate_document`, `generate_document`, `pause_document`, `cancel_document_flow`, `ambiguous`.

Модель выбирает реальные ID по названию и описанию. Единственный уверенный кандидат может создать draft; несколько кандидатов, отсутствие совпадения или `needs_clarification` требуют выбора/уточнения. Вопрос о каталоге сам draft не создаёт. Неизвестные ID/поля, противоречивый intent и некорректный JSON отклоняются.

Для document interpretation к существующему LLMClient добавлен необязательный `response_schema`. Остальные вызовы сохраняют прежний режим. Грамматика строится из переданной схемы, ограничивает template IDs, field IDs и транспортные типы. Внутренний ответ модели имеет пять свойств:

```json
{
  "intent": "answer_field",
  "template_ids": [],
  "changes": [{"field": "actual_field_key", "value": "значение"}],
  "target_field": "",
  "needs_clarification": false
}
```

Список changes позволяет извлекать несколько реквизитов независимо от порядка свойств JSON Schema. Python проверяет уникальность полей и преобразует список в map. Контракт Python → Go остаётся `{intent, template_ids, changes: object, target_field, needs_clarification}`. Go повторно проверяет whitelist и передаёт map в **существующий** `UpdateDraftFields`.

Новые повторяемые списки передаются массивом строк/объектов нужного типа. Для уже заполненного repeater модель использует `operations`: `append`, `update` с индексом от 1 и patch ячеек, `delete`, явный `replace`. Python применяет только эти структурные операции к snapshot, сохраняя остальные строки/ячейки. Это не отдельная validation: итоговый массив всё равно проходит TemplateService/v2 и CAS. Неизвестные операции, индексы, ячейки и повтор одного field ID отклоняются.

Вопросы берутся из `validation.next_question`; при явном редактировании — из активного `TemplateField.Question`. Conditional rules не вычисляются заново в чате. После каждого сохранения v2 возвращает актуальный набор полей. В LLM-контекст не попадают скрытые поля и их значения. Необязательные поля можно открыть через «Изменить»; обязательные нельзя обойти словами «не знаю». Неоднозначность оставляет draft без изменений и предлагает уточнение/обычную форму.

`knowledge_question` передаётся в прежний RAG-процесс. Document turns исключены из будущей RAG history. Отзыв доступа выключает document workflow, но позволяет продолжать обычные вопросы в этом чате без передачи данных отозванного draft интерпретатору.

## Публичный API и подтверждение

Все маршруты используют существующие authentication/origin/CSRF middleware.

| Маршрут | Назначение |
| --- | --- |
| `GET /api/v1/chat/sessions/{id}/document` | enabled, workflow, актуальный draft/validation, доступные options, artifact metadata; unavailable при отзыве доступа |
| `POST /api/v1/chat/sessions/{id}/document/actions` | Детерминированное действие внутри чата |
| `POST /api/v1/chat/sessions/{id}/messages` | Прежний маршрут; перед RAG добавлена document interpretation при включённом флаге |
| `GET /api/v1/generated-documents/{id}` | Существующие метаданные artifact |
| `GET /api/v1/generated-documents/{id}/download` | Существующая авторизованная выдача DOCX |

Тело action:

```json
{
  "client_message_id": "UUID",
  "action": "answer",
  "workflow_version": 4,
  "expected_revision": 7,
  "values": {"actual_field_key": "значение"}
}
```

Действия: `start`, `choose` (+ template_id), `answer` (+ values), `edit` (+ field_key), `progress`, `validate`, `generate`, `pause`, `resume`, `cancel`. Action возвращает сохранённый `ChatTurn`; frontend обновляет history и document view. Максимум JSON для document actions — 256 KiB, как у Wizard; итоговые values по-прежнему ограничены 64 KiB существующим сервисом. Ошибки размера — 413, устаревшая revision/version — 409, недоступный чужой объект — 404.

Каждое действие имеет UUID и SHA256 полного payload. Повтор UUID с тем же payload возвращает прежнюю пару сообщений; с другим payload — 409. Повтор обычного сообщения также не вызывает модель заново после успешного сохранения.

Summary показывает фактические values, серверные warnings/manual review и ApprovalRoute. Создание возможно только при `validation.ready` и подтверждении актуальной `reviewed_revision`. Изменение в Wizard требует повторно показать актуальные данные и подтвердить генерацию. Даже прямой HTTP action не обходит эту проверку. Скачиваемый файл обозначен как проект; в карточке указана редакция artifact.

## React и безопасность

`ChatDocumentPanel` размещён на уровне страницы, чтобы не нарушить границы features. Переиспользованы `TemplateFieldRenderer`, `ValidationReport`, `ApprovalRoute`, `valueText` и существующий authenticated download helper. Можно отвечать свободным текстом, пользоваться встроенной формой, видеть прогресс, изменить поле, отложить работу и открыть тот же draft в Wizard. Ссылка на файл отображается рядом с durable assistant message и после reload.

Owner/active-user/session checks, доступ к исходному документу, enabled template и ACL проверяются сервисами. Проверки повторяются при чтении draft, генерации, metadata и download. SQL-проекция истории скрывает **обе стороны** document turns и artifact IDs после ACL revoke/отключения шаблона. Имена кандидатов показываются по актуальному каталогу, а не копируются в несвязанную с draft историю.

LLM не может выполнять HTTP/SQL/команды, выдавать права, выбирать произвольные draft/artifact IDs или утверждать validation. Prompt injection остаётся текстом. Неизвестный field ID/тип не сохраняется; правила и renderer вызываются сервером. В новых логах — длительность/успех и фиксированная категория отклонения, без реквизитов, raw model response, паролей и токенов.

## Миграция и изоляция

`00013_chat_document_workflow.sql` добавляет workflow и ссылки/hash в chat_messages, разрешает mode=document. Старые сообщения/чаты/drafts не удаляются. Down преобразует новый mode в no_sources и удаляет новые связи/state. Перед удалением ACL-связей текст связанных с draft новых document turns заменяется нейтральным сообщением недоступности; иначе он мог бы открыться после revoke. Обычная история остаётся без изменений, все строки сообщений и настоящие drafts сохраняются. Down проверен до 12 с последующим Up до 13 в SQL integration test. Для эксплуатационного отключения предпочтителен feature flag, сохраняющий полное состояние.

Проверки выполнены в compose project `chat-documents-check`, отдельные volumes `chat_postgres`, `chat_documents`, БД `chat_documents_test`, PostgreSQL `127.0.0.1:15439`, React `http://localhost:18080`, Go `18081`, Python `18001`. Основные volumes не подключены. В конце проверки основная БД read-only показала migration **12**, изолированная — **13**. Основные API/PostgreSQL/AI services не перезапускались этой задачей.

Отдельный `restart api` выполнен только в изолированном проекте. Снимки двух workflow до/после полностью совпали: values, revisions, artifact references и число сообщений. Доказательства: `.local-integration/chat-documents/restart-before.json`, `restart-after.json`. Браузерная сессия после reload осталась работоспособной.

## Семь профилей: настоящий renderer и сравнение с Wizard

`scripts/chat_document_e2e.py` регистрирует исходные настоящие DOCX в изолированной БД. Используются реальные Go TemplateService и Python v2 Renderer, а существующий `template_v2_e2e.verify` проверяет результат.

| Профиль | Обязательных ответов | Проверено альтернатив choices | DOCX/download | Эквивалентность Wizard |
| --- | ---: | ---: | --- | --- |
| internal-audit | 15 | 3 | passed | passed |
| is-commissioning | 18 | 4 | passed | passed |
| local-document | 12 | 8 | passed | passed |
| project-committee | 13 | 5 | passed | passed |
| property-transfer | 16 | 11 | passed | passed |
| records-responsibles | 10 | 5 | passed | passed |
| working-group | 15 | 4 | passed | passed |

Итого 99 required answer saves и 40 альтернатив для активных choice-полей. На каждой альтернативе validation чата совпадает с validation Wizard; это не заявление о переборе всего декартова произведения условий.

Для каждого профиля созданы отдельные chat/Wizard drafts с одинаковыми values. Совпали values, активные/обязательные поля, полный validation включая warnings/routing, канонические `word/*.xml` generated DOCX. Проверены служебные маркеры, protected zones подписи/регистрации, repeating rows, SHA256, настоящий download, pause/resume/cancel, duplicate send, конкурентная генерация, CAS двух вкладок на уровне HTTP, правка через Wizard, foreign session/draft/artifact и ACL revoke. В каталоге `.local-integration/chat-documents/` сохранены семь DOCX, `e2e.json` и `fixtures.json`.

## Реальный браузер и полный диалог Qwen

Проверка выполнена через настоящий React в in-app browser; HTTP E2E не выдаётся за браузерный. Использованы только синтетические реквизиты.

Первый полный сценарий: sign-in → новый чат → свободный русский запрос → выбор реального working-group → заполнение → два участника → изменение руководителя → progress/validation/warnings/route → подтверждение → DOCX download → проверка ZIP/XML/значений → reload с сохранённой кнопкой → переход в Wizard → изменение того же draft → возврат в чат → новое подтверждение → DOCX следующей редакции.

Фрагменты фактического диалога:

> Пользователь: «Хочу оформить распоряжение: собрать межфункциональную команду для перехода на электронный архив. Помоги подготовить документ.»
>
> AI: «Подойдёт шаблон “Модуль_распоряжения_о_создании_рабочей_группы”. Начнём заполнение. Наименование организации?»
>
> Пользователь: «Организация — Учебная тестовая организация.»
>
> AI: «Ответ сохранён. Подписант: инициалы и фамилия?»
>
> Пользователь передал подписанта/должность, тему/цель/действия/результат, затем адресата, событие завершения, явно названную дату и ответственных. Каждая принятая группа значений увеличила прогресс настоящего draft.
>
> Пользователь: «Добавь двух членов группы. Первый: инженер отдела ИТ Смирнов Алексей Николаевич… Второй: главный специалист отдела ДОУ Кузнецова Елена Викторовна…»
>
> AI: «Ответ сохранён. Обязательные поля заполнены. Проверьте данные и предупреждения перед созданием проекта DOCX.»
>
> Пользователь: «Измени руководителя: теперь это Орлов Сергей Викторович, начальник управления информационных технологий.»
>
> После проверки и нажатия «Создать DOCX»: «Проект документа сформирован. Вы можете скачать файл и проверить его перед подписанием.»

Скачан реальный `generated-document.docx`, скопирован в `.local-integration/chat-documents/browser-downloaded.docx`, проверен существующим v2 verifier: нужный руководитель, две строки, сохранённые protected zones, SHA256 соответствует artifact. Исходная полная история и validation: `browser-dialogue.json`.

В Wizard руководитель изменён с начальника на директора управления, revision 7 → 8. В чате видны те же данные. Первый generate после Wizard потребовал новое подтверждение; второй сформировал новую редакцию. Обычный вопрос «Кто согласовывает…?» вернулся в RAG/no_sources, не изменяя draft. Поскольку тестовые документы не служат полноценной проиндексированной нормативной базой, no_sources здесь подтверждает маршрутизацию, а не качество содержательного RAG-ответа.

Скриншоты: `browser-review.jpg`, `browser-completed.jpg`, `browser-reloaded.jpg`, `browser-wizard-roundtrip.jpg` в том же каталоге.

Второй полный браузерный проход на окончательном контракте также завершён: session `42c0e571-fcc0-4e4e-80d4-4d2d04d0fc1c`, draft `1c286bd4-b6e0-4e85-9f92-d25a7eb914de`, revision 8, artifact `e009469d-3fc3-4355-acdc-9377ba88e83c`. После 15/15 обязательных полей изменён руководитель, выполнены progress/validation и подтверждение. Кнопка React скачала `generated-document (1).docx`; проверены v2 verifier, protected zones, изменённый руководитель, два участника, SHA256 и равенство с authenticated download endpoint. После следующего restart/reload история и карточка редакции 8 сохранились. Сохранены `browser-final-dialogue.json` (28 сообщений), `browser-final-downloaded.docx`, `browser-final-review.jpg`, `browser-final-completed.jpg`, `browser-final-reloaded.jpg`.

## Тесты, ошибки разработки и задержка

| Проверка | Фактический результат |
| --- | --- |
| `go test ./...` + TEST_DATABASE_URL изолированной PostgreSQL | passed, включая SQL integration suite |
| `go vet ./...` | passed |
| Linux `go test -race` service/postgres/aiclient/http/handlers | все пять пакетов passed |
| Python `pytest -q` с существующим локальным E5 | **243 passed**, без skipped |
| Приватный JSON/LLM document contract | **29 passed**; включены негативные list operations |
| React typecheck, ESLint, Vitest, production build | **119 passed**, 13 test files; сборка успешна |
| Основной и изолированный `docker compose config --quiet` | passed |
| Изолированная сборка Go/Python/React | passed |
| Семь профильных HTTP E2E | 7/7 passed |
| Реальная Qwen classification | повтор 12/12 passed; ранее один из 12 ответов отклонён как llm_invalid_response |
| Реальная Qwen field/workflow regression | 9/9 passed |

Набор classification: 4 development + 8 `independent_internal` в `scripts/chat_document_qwen_cases.json`. Последние формулировки отложены отдельно и не включены в prompt; это созданный Codex внутренний holdout, **не внешний blind test**. Prompt содержит универсальные намерения и транспортные правила, без названий семи модулей и фиксированной последовательности вопросов.

При разработке обнаружены и исправлены реальные ошибки: nested changes/person-object вместо строки, ограничение action body в 4 KiB, слишком свободная грамматика строк repeater, перестановка участника при полном пересоздании списка, пропуск второго изменения при зависящем от порядка JSON-объекте, блокировка обычного RAG после revoke. Для строк добавлены структурные операции, для изменений — упорядоченный пользователем список, для catalog IDs — динамический enum. Серверные правила не ослаблялись. Первые неуспешные Qwen-прогоны сохранены как `qwen-fields-initial.json`, `qwen-fields-before-operations.json`, `qwen-check-transient-failure.json`.

Во втором браузерном проходе несколько больших сообщений были безопасно отклонены интерпретатором; после разбиения данные сохранились. Это ограничение модели: прохождение тестового набора не гарантирует извлечение всех реквизитов из любой формулировки. Итоговый summary и форма остаются обязательной возможностью проверки/исправления.

Повторные служебные sign-in в тестах достигли существующего лимита 10/15 минут и получили 429. Ограничитель не менялся. Одно повторение pytest было ошибочно запущено из корня репозитория и завершилось collection/import errors; корректный запуск из ai-service прошёл полностью.

| Измерение | n | p50 | p95 |
| --- | ---: | ---: | ---: |
| Детерминированный action + актуальный view, без Qwen | 99 | 0,250 с | 0,312 с |
| Classification через реальную Qwen, последний полный прогон | 12 | 1,679 с | 1,719 с |
| Естественный field/workflow turn + сохранение + актуальный view | 9 | 1,688 с | 2,968 с |

Перцентили вычислены runner как median и элемент `sorted[int(n*0.95)-1]`; это малые локальные выборки, не нагрузочный SLA. Предыдущий classification-прогон при параллельной работе дал p95 5,672 с и один invalid response. Все числовые исходники — `e2e.json`, `qwen-check.json`, `qwen-fields.json`.

## Изменённые файлы этой задачи

В рабочем дереве уже были незакоммиченные RAG/context и UI redesign изменения. Они сохранены. Общий `git diff` не является перечнем исключительно этой задачи.

- Новые Go: `internal/domain/chat_document.go`, `internal/aiclient/chat_documents.go`, `internal/service/chat_documents.go`, `internal/repository/postgres/chat_document_repository.go` и их тесты; `internal/config/chat_documents_test.go`, `internal/transport/http/handlers/chat_documents_test.go`.
- Интеграция Go: `cmd/server/main.go`, `internal/config/config.go`, `internal/domain/chat_message.go`, `internal/service/chat.go`, `internal/repository/postgres/chat_repository.go`, `template_repository.go`, `documents_integration_test.go`, `internal/transport/http/router.go`, `handlers/chat.go`.
- Python: новые `ai-service/app/api/chat_documents.py`, `ai-service/tests/test_chat_documents.py`; подключение в `application.py`, private limit в `core/config.py`, необязательный schema argument в `llm/client.py`.
- React: новые `frontend/src/pages/ChatPage/ChatDocumentPanel.tsx`, `.module.css`, `.test.tsx`; интеграция в `ChatPage.tsx`, `features/chat/types/chat.ts`, `components/ChatMessage.tsx`/`.test.tsx`, `components/ChatInput.tsx`; экспорт общих компонентов в `features/templates/index.ts`.
- Инфраструктура: `migrations/00013_chat_document_workflow.sql`, `.env.example`, `docker-compose.yml`, `scripts/chat-document-compose.yml`.
- Проверки/отчёт: `scripts/chat_document_e2e.py`, `chat_document_qwen_cases.json`, `chat_document_qwen_check.py`, `chat_document_qwen_fields_check.py`, этот документ.

## Ограничения и безопасное включение

1. Qwen остаётся вероятностным интерпретатором. Возможны отказ/неполное извлечение; данные нужно проверять в summary. Неоднозначные/невалидные ответы не обходят validation, доступна встроенная форма. Ошибка модели до начала workflow возвращает запрос в прежний RAG; начать подготовку можно кнопкой.
2. Один текущий workflow на chat session; прежние drafts сохраняются в Wizard. Каталог ограничен 200, число полей — существующими лимитами v2. Более крупный каталог требует отдельного поиска кандидатов.
3. Естественное сообщение ограничено существующим chat limit 500 символов. Большие списки вводятся несколькими сообщениями или формой. Итоговые values — 64 KiB.
4. Две вкладки проверены конкурентными HTTP-запросами и CAS; отдельный сценарий с двумя одновременно управляемыми браузерными вкладками не выполнялся. Ручное визуальное открытие всех семи DOCX в Microsoft Word не выполнялось; проверены XML/структура/защищённые зоны существующим verifier.
5. Внешнего blind test и длительного нагрузочного/аварийного soak test не было. Все результаты относятся к изолированному стенду. Unit-проверки не используются как единственное основание для готовности.
6. Down миграции теряет новые ACL-связи и намеренно скрывает текст связанных новых document turns; реквизиты остаются в draft. Для обратимого отключения использовать feature flag, сохраняя схему и историю.

Для безопасной проверки:

```powershell
# Подготовить отдельный ignored env_file только с разрешёнными LLM_* настройками
# текущей Qwen и внутренним тестовым AI_SERVICE_TOKEN; не копировать секреты в Git.
docker compose -p chat-documents-check -f scripts/chat-document-compose.yml config --quiet
docker compose -p chat-documents-check -f scripts/chat-document-compose.yml build
docker compose -p chat-documents-check -f scripts/chat-document-compose.yml up -d
```

Изолированный compose требует `.local-integration/chat-documents/ai.env`. Тестовые bootstrap credentials и токены в этом compose предназначены только для localhost стенда; в основном окружении их использовать нельзя. Тестовые fixtures содержат синтетические credentials и поэтому весь каталог остаётся ignored.

Для последующего разрешённого включения в целевом окружении нужны backup/обычный процесс миграции до 13, зарегистрированные enabled templates с ACL, работающие AI_SERVICE_URL/TOKEN и существующая Qwen с поддержкой json_schema. Затем включается `CHAT_DOCUMENT_CREATION_ENABLED=true` и выполняется smoke: новый чат → draft → validation → подтверждение → download → reload. Context Resolver можно оставить false. Эти действия в основном окружении в рамках задачи **не выполнялись**. Для выключения вернуть флаг false; Wizard и RAG продолжают работать.
