// Development-only visual fixtures. This entry is excluded from the production build.
// No API is mocked, no requests are sent, and no draft is saved.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import '../src/shared/styles/global.css';
import { ChatMessage } from '../src/features/chat';
import {
  TemplateFieldRenderer,
  ValidationReport,
  type FieldValue,
  type TemplateProfile,
} from '../src/features/templates';
import templateStyles from '../src/features/templates/styles/templates.module.css';
import ui from '../src/shared/ui/ui.module.css';
import { LoginPage } from '../src/pages/LoginPage/LoginPage';
import profilesData from './profiles.json';

const profiles = profilesData as TemplateProfile[];
const client = new QueryClient({ defaultOptions: { queries: { enabled: false } } });
client.setQueryData(['auth', 'me'], null);
const longAnswer =
  '# Порядок подготовки документа\n\n' +
  Array.from(
    { length: 8 },
    (_, i) =>
      `## ${i + 1}. Проверка реквизитов\n\nЭто **изолированный текст для проверки вёрстки**, не ответ сервиса. Проверяем читаемость длинных абзацев, перенос строк и расстояния между блоками.\n\n1. Подготовьте проект документа.\n2. Проверьте состав участников.\n3. Сохраните результат проверки.\n\n| Этап | Результат |\n| --- | --- |\n| Подготовка | Проект документа |\n| Проверка | Замечания к проекту |\n`,
  ).join('\n');

function VisualReview() {
  const [mode, setMode] = useState('wizard');
  const [profileIndex, setProfileIndex] = useState(0);
  const [values, setValues] = useState<Record<string, FieldValue>>({});
  const profile = profiles[profileIndex]!;
  return (
    <QueryClientProvider client={client}>
      <main className={templateStyles['templates-page']} style={{ maxWidth: 1040 }}>
        <div className={ui['page-heading']}>
          <div>
            <h1>Визуальная проверка</h1>
            <p>
              Изолированные данные. Эта страница не входит в приложение и не сохраняет изменения.
            </p>
          </div>
        </div>
        <div className={templateStyles['template-actions']}>
          <button className={ui.button} onClick={() => setMode('wizard')}>
            Форма с ошибками
          </button>
          <button className={ui.button} onClick={() => setMode('markdown')}>
            Длинный ответ Markdown
          </button>
        </div>
        {mode === 'markdown' ? (
          <ChatMessage
            message={{
              id: 'visual',
              role: 'assistant',
              content: longAnswer,
              mode: 'generated',
              sources: [],
            }}
          />
        ) : (
          <>
            <label>
              Профиль шаблона
              <select
                value={profileIndex}
                onChange={(event) => {
                  setProfileIndex(Number(event.target.value));
                  setValues({});
                }}
              >
                {profiles.map((item, i) => (
                  <option key={item.profile_id} value={i}>
                    {item.profile_id}
                  </option>
                ))}
              </select>
            </label>
            <p role="status">
              {profile.fields.length} полей · редакция {profile.revision}
            </p>
            <section className={templateStyles['template-panel']}>
              <h2>Проверьте обязательные поля</h2>
              {profile.fields.slice(0, 4).map((field) => (
                <TemplateFieldRenderer
                  key={field.key}
                  field={field}
                  value={values[field.key] ?? ''}
                  onChange={(value) => setValues({ ...values, [field.key]: value })}
                  required={field.required}
                  error={
                    field.required && !values[field.key]
                      ? 'Заполните обязательное поле.'
                      : undefined
                  }
                />
              ))}
            </section>
            {profile.fields
              .filter((field) => ['repeater', 'person_list', 'date'].includes(field.type))
              .map((field) => (
                <section key={field.key} className={templateStyles['template-panel']}>
                  <TemplateFieldRenderer
                    field={field}
                    value={values[field.key] ?? (field.type === 'date' ? '' : [])}
                    onChange={(value) => setValues({ ...values, [field.key]: value })}
                    required={field.required}
                  />
                </section>
              ))}
            <ValidationReport
              report={{
                ready: false,
                next_question: null,
                missing_fields: profile.fields.filter((field) => field.required).slice(0, 2),
                invalid_fields: [],
                checks: [],
              }}
              fields={profile.fields}
            />
          </>
        )}
      </main>
    </QueryClientProvider>
  );
}
createRoot(document.getElementById('root')!).render(
  new URLSearchParams(window.location.search).get('screen') === 'login' ? (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LoginPage />
        <aside style={{ position: 'fixed', bottom: 8, left: 12, fontSize: 13, background: '#fff', padding: 6 }}>
          Визуальная проверка · изолированное состояние без сессии
        </aside>
      </MemoryRouter>
    </QueryClientProvider>
  ) : <VisualReview />,
);
