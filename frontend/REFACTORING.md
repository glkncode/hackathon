# Архитектурный рефакторинг frontend

Дата проверки: 24 октября 2026 года. Область изменений — `frontend/`.

## 1. Результаты аудита

Исходные проверки проходили: TypeScript, ESLint, 95 тестов в 8 файлах и production-сборка.
Рефакторинг устраняет подтверждённые проблемы организации кода:

- API, DTO, компоненты и хуки одной функциональности находились в разных общих каталогах; папки `features` преимущественно содержали `.gitkeep`.
- Страницы чата, входа и шаблонов совмещали отображение, запросы, мутации, навигацию и обновление Query cache. Большой файл черновика также содержал самостоятельные компоненты редактора и прогресса.
- Ключ авторизации и очистка сессии находились в провайдерах приложения, хотя использовались функциональными модулями.
- Скачивание источника связывало чат с API документов, а API документов — с DTO источника чата.
- Общий CSS смешивал layout, авторизацию, чат, документы, шаблоны, состояния и адаптивность. Порядок правил был частью поведения интерфейса.

Наличие циклических импортов заранее не предполагалось. Итоговый граф TypeScript-импортов проверяется отдельным тестом.

## 2. Изменения и границы ответственности

| Область | Изменения |
| --- | --- |
| `app` | Провайдеры, маршрутизация, композиция приложения и основной layout. Провайдер реагирует на истечение сессии через API auth feature. |
| `auth` | API, `User`, ProtectedRoute, useAuth/useLogout, useLogin, ключ и очистка сессии. Проверка актуального владельца через QueryClient не создаёт дополнительного запроса. |
| `chat` | API чата и временного поиска, DTO, история и сессии, ChatMessage, ChatInput, ChatWelcome, SourceCard. useChatController управляет отправкой, отменой, идемпотентностью, fallback, архивированием и прокруткой. |
| `documents` | API документов и версий, DTO, пагинация, строки документов и загрузка. Ключи кеша принадлежат feature. |
| `templates` | API и DTO, каталог, загрузка согласованной ревизии черновика, редактор, поля, прогресс, проверки, маршрут согласования и результат. Контроллеры вынесены из страниц. |
| `pages` | Композиция UI и логика конкретного маршрута. Регистрация шаблона соединяет documents и templates в хуке страницы. Страница черновика передаёт идентификатор и проверку владельца редактору. |
| `shared` | HTTP/CSRF transport, скачивание точной версии документа, сохранение файла, `cx`, универсальные сообщения об ошибке/загрузке и `Collection`. |

Фичи не импортируют друг друга. `shared` не зависит от `features`, `pages` или `app`.
Внешний TypeScript API каждой feature явно экспортируется из одного `index.ts`; внутренние импорты прямые.
Общие CSS Modules шаблонов используются их страницами напрямую, без экспорта CSS через TypeScript barrel.
Состояние отдельных полей и загрузки файлов остаётся рядом с соответствующим UI, где оно относится к жизненному циклу формы.

Сохранены URL и методы API, тела запросов, DTO, CSRF, cookies, обработка ошибок, AbortSignal, порядок операций и скачивание файлов.
HTTP-клиент, API auth/chat/search/templates, исходные query-хуки и утилита скачивания сравнены с исходными версиями по синтаксическому дереву без импортов и форматирования: реализации совпадают.
В documents API только скачивание процитированной версии делегировано общему transport с прежним URL; прежний метод `downloadSource` сохранён.

Ключи `auth/me`, `chat/sessions`, `chat/history`, `documents`, `templates/catalog`, `templates/drafts`, `templates/draft`, `templates/detail`, `templates/result`, `template-source-documents`, `template-source-document`, параметры кеширования и инвалидации сохранены.
Сохранены защита от позднего сохранения после выхода, обработка конфликтов ревизий, ручное обновление черновика и защита от повторного запуска мутаций.
История чата перечитывается с актуальными данными сервера; локальное восстановление закрытого текста и персистентное хранение не добавлялись.
Все четыре режима ответов и безопасный текст ACL сохраняют прежнее отображение.

Старые каталоги реализаций и ненужные `.gitkeep` удалены. Новых зависимостей, альтернативной реализации приложения, изменений backend или инфраструктуры нет.

## 3. Итоговая структура src

```text
src/
├── app/
│   ├── __tests__/
│   │   ├── apiTransport.test.ts
│   │   └── architecture.test.ts
│   ├── layout/
│   │   ├── AppLayout.module.css
│   │   ├── AppLayout.test.tsx
│   │   ├── AppLayout.tsx
│   │   ├── Header.module.css
│   │   ├── Header.tsx
│   │   ├── Sidebar.module.css
│   │   └── Sidebar.tsx
│   ├── App.tsx
│   ├── providers.tsx
│   └── router.tsx
├── features/
│   ├── auth/
│   │   ├── api/
│   │   │   └── auth.ts
│   │   ├── components/
│   │   │   └── ProtectedRoute.tsx
│   │   ├── hooks/
│   │   │   ├── useAuth.ts
│   │   │   ├── useCurrentUserGuard.ts
│   │   │   └── useLogin.ts
│   │   ├── model/
│   │   │   ├── session.test.ts
│   │   │   └── session.ts
│   │   ├── types/
│   │   │   └── auth.ts
│   │   └── index.ts
│   ├── chat/
│   │   ├── api/
│   │   │   ├── chat.ts
│   │   │   └── search.ts
│   │   ├── components/
│   │   │   ├── ChatInput.module.css
│   │   │   ├── ChatInput.tsx
│   │   │   ├── ChatMessage.module.css
│   │   │   ├── ChatMessage.test.tsx
│   │   │   ├── ChatMessage.tsx
│   │   │   ├── ChatWelcome.module.css
│   │   │   ├── ChatWelcome.tsx
│   │   │   ├── SourceCard.module.css
│   │   │   └── SourceCard.tsx
│   │   ├── hooks/
│   │   │   ├── useChatController.ts
│   │   │   ├── useChatHistory.ts
│   │   │   └── useChatSessions.ts
│   │   ├── types/
│   │   │   └── chat.ts
│   │   └── index.ts
│   ├── documents/
│   │   ├── api/
│   │   │   └── documents.ts
│   │   ├── components/
│   │   │   ├── DocumentRow.module.css
│   │   │   ├── DocumentRow.tsx
│   │   │   ├── UploadDocument.module.css
│   │   │   └── UploadDocument.tsx
│   │   ├── hooks/
│   │   │   ├── keys.ts
│   │   │   └── useDocuments.ts
│   │   ├── types/
│   │   │   └── documents.ts
│   │   └── index.ts
│   └── templates/
│       ├── api/
│       │   ├── templates.test.ts
│       │   └── templates.ts
│       ├── components/
│       │   ├── DraftCard.tsx
│       │   ├── DraftProgress.module.css
│       │   ├── DraftProgress.tsx
│       │   ├── FieldEditor.tsx
│       │   ├── TemplateBadge.module.css
│       │   ├── TemplateBadge.tsx
│       │   ├── TemplateCard.module.css
│       │   ├── TemplateCard.tsx
│       │   ├── TemplateDraft.module.css
│       │   ├── TemplateDraft.tsx
│       │   ├── TemplateFieldRenderer.module.css
│       │   ├── TemplateFieldRenderer.tsx
│       │   ├── ValidationReport.module.css
│       │   └── ValidationReport.tsx
│       ├── hooks/
│       │   ├── useTemplateCatalogController.ts
│       │   ├── useTemplateDraftController.ts
│       │   ├── useTemplateResult.ts
│       │   └── useTemplates.ts
│       ├── lib/
│       │   └── templateDisplay.ts
│       ├── styles/
│       │   └── templates.module.css
│       ├── types/
│       │   └── templates.ts
│       └── index.ts
├── pages/
│   ├── __tests__/
│   │   └── Templates.test.tsx
│   ├── ChatPage/
│   │   ├── ChatPage.module.css
│   │   ├── ChatPage.scroll.test.tsx
│   │   ├── ChatPage.test.tsx
│   │   └── ChatPage.tsx
│   ├── DocumentsPage/
│   │   ├── DocumentsPage.module.css
│   │   └── DocumentsPage.tsx
│   ├── LoginPage/
│   │   ├── LoginPage.module.css
│   │   └── LoginPage.tsx
│   ├── NotFoundPage/
│   │   ├── NotFoundPage.module.css
│   │   └── NotFoundPage.tsx
│   ├── TemplateDraftPage/
│   │   └── TemplateDraftPage.tsx
│   ├── TemplateRegisterPage/
│   │   ├── TemplateRegisterPage.module.css
│   │   ├── TemplateRegisterPage.tsx
│   │   └── useTemplateRegistration.ts
│   ├── TemplateResultPage/
│   │   ├── TemplateResultPage.module.css
│   │   └── TemplateResultPage.tsx
│   └── TemplatesPage/
│       ├── TemplatesPage.module.css
│       └── TemplatesPage.tsx
├── shared/
│   ├── api/
│   │   ├── client.ts
│   │   └── documentFiles.ts
│   ├── lib/
│   │   ├── cx.ts
│   │   ├── download.test.ts
│   │   └── download.ts
│   ├── styles/
│   │   └── global.css
│   ├── types/
│   │   └── collection.ts
│   └── ui/
│       ├── Feedback.tsx
│       └── ui.module.css
└── main.tsx
```

## 4. CSS

Локальные стили распределены по 24 файлам `*.module.css` рядом с компонентами и страницами.
Общие правила шаблонов находятся внутри templates feature. Универсальные кнопки, типографические метки, формы и уведомления — в `shared/ui/ui.module.css`.

В `shared/styles/global.css` оставлены Tailwind, настройка шрифта, базовые правила элементов, focus-visible, сброс отступов и глобальная поддержка reduced motion.
Компонентных селекторов в глобальном файле нет. Tailwind сохранён, в частности для `animate-spin`.

Каскад после слоёв Tailwind задан как `app-base → app-ui → app-components`.
Это сохраняет приоритет локальных настроек относительно общих кнопок независимо от порядка загрузки модулей.
Для межмодульных селекторов используются `@value`, без `:global` и новых `!important`.
Сохранены media queries, состояния hover/focus/disabled, адаптивные сетки, переносы текста, анимации и размеры.
Сравнение исходного CSS и распределённых модулей через PostCSS подтвердило сохранение деклараций для всех 391 сочетаний селектора и media query.
Правила sr-only включают прежние свойства Tailwind, чтобы локализация класса не меняла доступность и геометрию.

В Vite заданы имена CSS-классов, стабильные относительно пути и локального имени. Изменение содержимого общего CSS при HMR не рассогласует его классы и селекторы потребителей.
Production-минификация использует Lightning CSS, уже установленный в зависимостях Tailwind: он объединяет повторные импорты правил и слоёв. `package.json` и lockfile не менялись.
Итоговый CSS — около 34,6 КБ, gzip около 8,8 КБ.

## 5. Проверки

Команды выполнялись из `frontend/` через npm 11.6.2, запущенный имеющимся pnpm runtime: системный npm отсутствует в PATH. Выполнены именно скрипты `package.json`.

| Команда | Результат |
| --- | --- |
| `npm run typecheck` | Успешно, строгая проверка TypeScript |
| `npm run lint` | Успешно, без отключения правил |
| `npm run test` | 100 тестов в 10 файлах: 95 прежних и 5 новых |
| `npm run build` | Успешно, TypeScript + production Vite build |

Новые регрессии проверяют направления зависимостей, public TypeScript API фич, отсутствие циклов, полный список маршрутов и мобильный focus trap с CSS Modules, включая Escape, восстановление фокуса и один запрос истории.
Существующие проверки auth, CSRF, скачивания, всех режимов ответа, ACL, обновления истории, длинного чата, конфликтов черновика, валидации, генерации и регистрации шаблона сохранены.
Тесты, использовавшие CSS-классы, теперь обращаются к экспорту CSS Module; ожидаемое поведение не ослаблялось.

В браузере проверены dev-версия и локальная production-сборка с настоящими сохранёнными данными Go API:

- Чат на десктопе и при 390×844, сохранённый ответ и карточка источника.
- Список документов и каталог шаблонов: сравнение с исходными DOM-снимками, геометрией и вычисленными стилями.
- Мобильная навигация и каталог: совпадение с исходной production-версией.
- Сохранённый заполненный черновик, отчёт проверок и редактирование поля без отправки изменений на сервер.

Для чата, документов, каталога и формы редактора различий проверяемых свойств не обнаружено.
В отчёте черновика отмечены только субпиксельные различия ширины текстовых span менее 0,04 px, без изменения стилей и переноса строк; сравнение с допуском 1 px прошло.
Проверка длинного чата, обновления истории, очистки сессии и шаблонных мутаций также входит в существующие автоматические регрессии.

## 6. Оставшиеся риски и пределы проверки

Браузерная проверка проводилась в Chromium под текущей сессией member. Safari, Firefox, экранная клавиатура реального телефона и все промежуточные ширины отдельно не проверялись.
В рамках этого рефакторинга не повторялись реальные загрузка, регистрация шаблона, генерация DOCX, смена ACL и выход из действующей пользовательской сессии. Их frontend-сценарии покрыты сохранёнными тестами с подменённым API; это не новый полный backend E2E.
Длинные тексты и оставшиеся формы рекомендуется включить в обычный ручной приёмочный прогон.

В рефакторинг включены только файлы frontend. Существовавшие до работы изменения вне frontend сохранены.
