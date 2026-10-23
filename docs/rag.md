# Go → Python RAG

Go интегрирован с приватным `POST /internal/v1/rag/generate` по контракту ниже.
По согласованию endpoint уже реализуется/реализован Python-разработчиком, но его
код ещё не получен в этом checkout. Go-тесты используют HTTP mock, Python-код
не меняется. После получения Python-коммита нужно выполнить совместную проверку
этого контракта. До доступности endpoint ответы 404/503 дают search fallback.

## Конфигурация

```dotenv
AI_SERVICE_URL=http://127.0.0.1:8000
AI_SERVICE_TOKEN=<тот же приватный токен, что у Python>
RAG_ENABLED=false
RAG_TIMEOUT=60s
RAG_MAX_SOURCES=8
RAG_MAX_HISTORY_MESSAGES=10
```

RAG по умолчанию отключён. При включении обязательны URL и непустой токен;
некорректная конфигурация отклоняется при старте без вывода секретов.
RAG_TIMEOUT допустим от 1s до 1h, RAG_MAX_SOURCES — 1–30, история — 0–50
сообщений (0 выключает её передачу). Настройки ingestion остаются независимыми.

Один aiclient и один Transport обслуживают ingestion и RAG. Оба метода имеют
собственные context deadline, HTTP timeout ограничивает общий запрос;
redirect и ambient proxy отключены. В main write timeout HTTP-сервера увеличен
до RAG_TIMEOUT + 10s, если стандартных 30s недостаточно для ответа/fallback.

## Приватный контракт

```http
POST /internal/v1/rag/generate
Content-Type: application/json
Accept: application/json
X-Service-Token: <AI_SERVICE_TOKEN>
```

Тело (пример формы; IDs и текст иллюстративные):

```json
{
  "query": "Как согласовать служебную записку?",
  "history": [
    {"role":"user","content":"Какие правила согласования?"},
    {"role":"assistant","content":"Найдены подходящие положения инструкции."}
  ],
  "sources": [
    {
      "chunk_id":"bf65f1ad-7db9-44af-b891-202fc879502f",
      "document_title":"Инструкция",
      "version_number":1,
      "section_number":"4.6",
      "page_number":13,
      "content":"Текст сохранённого положения"
    }
  ]
}
```

query — текущий вопрос, до 500 Unicode-символов. История — только `role` и
`content`, без source cards, идентификаторов пользователя и диалога.
Источники — точная минимальная проекция retrieval; отсутствующие page/section
равны null. В теле нет ролей доступа, user_id, cookies, storage_key и credentials.
Сервисный токен передаётся только в приватном заголовке аутентификации.

Python должен возвращать HTTP 200, Content-Type application/json и **все четыре
поля**, включая null и пустые массивы:

```json
{
  "status":"answered",
  "answer":"Порядок согласования предусматривает...",
  "used_source_ids":["bf65f1ad-7db9-44af-b891-202fc879502f"],
  "clarification_question":null
}
```

| status | Ограничения | Chat mode |
| --- | --- | --- |
| answered | Непустой answer; хотя бы один source ID; clarification_question null/"" | generated |
| needs_clarification | Непустой clarification_question; answer null/""; source IDs могут быть [] | clarification |
| insufficient_information | answer и clarification_question null/""; used_source_ids [] | no_sources |

used_source_ids — уникальные непустые UUID **только из sources этого запроса**.
Вымышленные, неизвестные или повторные IDs отвергаются целиком. Для answered
сохраняются цитаты в порядке used_source_ids. Для insufficient_information Go
возвращает собственный фиксированный no_sources текст без содержания модели.

Ответ обязан быть одним JSON-объектом. Неизвестные/пропущенные поля, дубликаты
ключей, неверный регистр имён, неверные типы, trailing JSON, неизвестный статус,
несогласованные поля и невалидный UTF-8 отклоняются. answer/question ограничены
8000 Unicode-символов, 32 KiB, без NUL и пустой строки после TrimSpace. Размер
HTTP response — максимум 64 KiB, независимо от Content-Length; ответ читается
ограниченным Reader. Ограничена также вложенность JSON.

## Chat API и fallback

Публичные маршруты и request/response DTO [Chat API](chat.md) сохранены.
Генерация происходит внутри
`POST /api/v1/chat/sessions/{id}/messages`; отдельного публичного endpoint нет.
К существующим search/no_sources добавлены generated/clarification.

После проверки владельца и client_message_id Go вызывает SearchService
напрямую. Пустой retrieval сразу даёт no_sources **без Python и без загрузки
LLM-контекста**. При выключенном RAG сохраняется прежнее поведение search.
При включённом RAG Go получает свежую ограниченную историю с ACL, строит
ограниченный запрос и вызывает ChatGenerator.Generate.

Сетевые ошибки, 404/5xx, timeout, невалидный JSON, неизвестный ID и прочие ошибки
модели дают существующий `mode: search`, фиксированный текст
«Найдены подходящие положения инструкции.» и доступные реальные источники.
Невалидный generated текст не сохраняется. Автоматических HTTP-повторов нет.
Отмена клиентского контекста прерывает обработку без записи нового сообщения;
таймаут только генератора оставляет время сохранить search fallback.

Ошибки БД не подменяются ответом модели. При изменении ACL между retrieval,
генерацией и сохранением вся пара откатывается с 409
`sources changed; retry the message`. Тот же client_message_id можно повторить.
Если другой конкурентный запрос с этим ключом уже сохранил безопасный результат,
возвращается он, после актуальной проверки ACL.

## ACL и сохранённые генеративные ответы

Поиск проверяет текущую активность/роль пользователя и Document ACL в SQL.
Исторический контекст читается непосредственно перед генерацией, после поиска.
Python получает только разрешённые фрагменты и разрешённый текст истории;
сам он не выполняет SQL-поиск и не принимает решений по ACL.

Нельзя полагаться только на used_source_ids: модель видела и не процитированные
источники. Поэтому миграция [00007_chat_rag.sql](../migrations/00007_chat_rag.sql)
создаёт `chat_message_dependencies(message_id,chunk_id)` с настоящими FK.
Для generated **и clarification** сохраняются все переданные retrieval IDs и
транзитивные зависимости включённых в контекст предыдущих generated/clarification
ответов. Полный текст фрагментов в provenance не дублируется.

При каждом чтении истории, RecentMessages, идемпотентном повторе и ответе после
commit SQL проверяет текущий ACL **всех** этих зависимостей. Если хотя бы одна
закрыта или provenance отсутствует, исходный content не выбирается из БД в Go.
Вместо него возвращается:

> Содержимое ответа недоступно из-за изменения прав доступа к документам.

Карточки такого ответа также скрываются целиком. IDs сообщений, timestamps и
исходный mode сохраняются; закрытые dependency IDs являются внутренними и не
сериализуются. В следующий запрос к модели такой ответ вообще не включается.
Повторное предоставление доступа снова делает сохранённый ответ доступным.
Решение использует текущую роль из users, поэтому понижение admin также учитывается.

Текст, provenance и cards читаются в одном коротком REPEATABLE READ snapshot.
Запись user + assistant + citations + dependencies атомарна, под блокировкой
диалога; транзакции не удерживаются во время поиска или HTTP-вызова Python.
Проверка существования/ACL повторяется при сохранении каждой зависимости.
Права применяются по снимку SQL-запроса: уже отправленный модели/клиенту текст
нельзя отозвать задним числом при конкурентном изменении ACL.

Идемпотентность остаётся прежней: одинаковый ключ и нормализованный вопрос
возвращают сохранённый результат без новой генерации; другой вопрос — 409.
Конкурентные запросы могут вызвать модель несколько раз, но сохранят одну пару.
Обычный admin по-прежнему не может читать чужие диалоги.

## Ограничения и prompt injection

- По умолчанию максимум 8 retrieval chunks и 10 сообщений; жёсткие потолки 30/50.
- Максимум 128 KiB текста и метаданных контекста, 1 MiB сериализованного JSON.
- Максимум 256 транзитивных chunk-зависимостей на генерируемое сообщение.
- При нехватке бюджета исключаются целые chunks/старые сообщения; цитаты не
  обрезаются и не получают выдуманных метаданных. Приоритет у retrieval и недавней истории.

Вопрос, история и документы являются недоверенными данными. Go не исполняет
ответы/команды модели, не предоставляет tools, не меняет ACL, роли, URL или SQL
по сгенерированному тексту. Ответ может стать только текстом assistant и ссылками
из allowlist. Неизвестные поля вроде tool_calls/command отвергаются. Python должен
отделять системные инструкции от context data в своём prompt; Go-проверки сами
по себе не доказывают достоверность пересказа и не заменяют эту границу.

Zap пишет только IDs, режим, длительность, число источников и безопасный код
причины fallback. Вопросы, документы, модельный текст, тела ошибок, URL и токены
не логируются.

Миграции 1–6 не меняются. Миграция 7 расширяет modes и предел assistant.content.
Её Down сохраняет пользовательскую историю, но заменяет generated/clarification
на нейтральный no_sources текст перед возвратом старых ограничений: старый backend
не должен получить сохранённый секретный пересказ без проверки provenance.

## Локальная проверка

Настройте DATABASE_URL, ADMIN_PASSWORD и private service credentials в окружении.
Для проверки без Python оставьте RAG_ENABLED=false и запустите `go run ./cmd/server`.
Для проверки реального endpoint после получения Python-кода:

```powershell
$env:RAG_ENABLED='true'
$env:RAG_TIMEOUT='60s'
$env:RAG_MAX_SOURCES='8'
$env:RAG_MAX_HISTORY_MESSAGES='10'
go run ./cmd/server
```

AUTO_MIGRATE=true применит миграцию 7. Сначала должны быть ready chunks и ACL;
тестовый пользователь входит, создаёт диалог и отправляет запрос через Chat API.
Проверять отзыв ACL нужно под member: admin имеет доступ ко всем документам.
Пример запросов в другом PowerShell (development, локальный доверенный Origin):

```powershell
$base='http://localhost:8080'
$headers=@{Origin=$base}
$login=@{username='admin';password=$env:ADMIN_PASSWORD}|ConvertTo-Json
Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Headers $headers -ContentType 'application/json' -Body $login -SessionVariable chatBrowserSession
$headers['X-CSRF-Token']=$chatBrowserSession.Cookies.GetCookies([uri]$base)['ai_csrf'].Value
$chat=Invoke-RestMethod "$base/api/v1/chat/sessions" -Method Post -WebSession $chatBrowserSession -Headers $headers -ContentType 'application/json' -Body '{}'
$question=@{content='Как согласовать служебную записку?';client_message_id=[guid]::NewGuid().ToString()}|ConvertTo-Json
Invoke-RestMethod "$base/api/v1/chat/sessions/$($chat.id)/messages" -Method Post -WebSession $chatBrowserSession -Headers $headers -ContentType 'application/json' -Body ([Text.Encoding]::UTF8.GetBytes($question))
```

Ожидается generated/clarification при соответствующем валидном Python-ответе,
no_sources при пустом retrieval/insufficient_information, search при выключенном
RAG или ошибке модели. Повторьте тот же `$question` для проверки идемпотентности.

Автотесты не требуют настоящей LLM или Python. PostgreSQL-тесты принимают только
явный TEST_DATABASE_URL с именем БД, заканчивающимся на `_test`, и используют
изолированные схемы:

```powershell
$env:TEST_DATABASE_URL='postgres://postgres:<test-password>@127.0.0.1:<test-port>/rag_test?sslmode=disable'
go mod tidy
go test ./...
go vet ./...
go build ./cmd/server
```

Основные файлы изменений: internal/aiclient/client.go и generate*.go,
internal/service/chat.go и chat_rag*.go, internal/domain/chat_message.go,
postgres/chat_repository.go и chat_rag_integration_test.go, internal/config/*,
cmd/server/main*.go, .env.example, migration 7, tests/integration/chat_rag_test.go,
эта документация и уточнения docs/chat.md. Тесты миграций учитывают версию 7.
