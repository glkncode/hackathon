import { Link } from 'react-router-dom';
import { ApiError } from '../../../shared/api/client';
import { cx } from '../../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { useTemplateDraftController } from '../hooks/useTemplateDraftController';
import { moduleTitle, progress, valueText, visibleFields } from '../lib/templateDisplay';
import templateStyles from '../styles/templates.module.css';
import { DraftProgress } from './DraftProgress';
import { FieldEditor } from './FieldEditor';
import { TemplateBadge } from './TemplateBadge';
import styles from './TemplateDraft.module.css';
import { ApprovalRoute, ValidationReport } from './ValidationReport';

export function TemplateDraftEditor({
  id,
  isCurrentOwner,
}: {
  id: string;
  isCurrentOwner: (ownerId: string) => boolean;
}) {
  const {
    query,
    draft,
    template,
    review,
    setReview,
    selected,
    setSelected,
    saved,
    generateLock,
    validate,
    generate,
    edit,
    onSaved,
  } = useTemplateDraftController(id, isCurrentOwner);
  if (query.isPending)
    return (
      <div className={templateStyles['templates-page']}>
        <Loading label="Загружаем сохранённый черновик…" />
      </div>
    );
  if (
    !draft ||
    (query.isError &&
      query.error instanceof ApiError &&
      [401, 403, 404].includes(query.error.status))
  )
    return (
      <div className={templateStyles['templates-page']}>
        <Link to="/templates" className={ui['text-button']}>
          ← К шаблонам
        </Link>
        <ErrorNotice error={query.error} onRetry={() => void query.refetch()} />
      </div>
    );
  if (!draft.validation)
    return (
      <div className={templateStyles['templates-page']}>
        <p role="status">Ответ сохранён · версия {draft.revision}</p>
        {query.isError ? (
          <ErrorNotice error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <Loading label="Получаем следующий вопрос…" />
        )}
      </div>
    );
  if (template.isError)
    return (
      <div className={templateStyles['templates-page']}>
        <ErrorNotice error={template.error} onRetry={() => void template.refetch()} />
        <Link to="/templates">К доступным шаблонам</Link>
      </div>
    );
  const report = draft.validation!;
  const fields = visibleFields(draft);
  const current = selected
    ? draft.fields.find((field) => field.key === selected)
    : !review
      ? report.next_question
      : undefined;
  const editing = selected !== null;
  const busy = query.isFetching || query.isError || validate.isPending || generate.isPending;
  const previous = fields
    .filter((field) => field.key !== current?.key && draft.values[field.key] !== undefined)
    .at(-1);
  return (
    <div className={templateStyles['templates-page']}>
      <Link to="/templates" className={ui['text-button']}>
        ← Шаблоны и мои черновики
      </Link>
      <div className={ui['page-heading']}>
        <div>
          <span className={ui['eyebrow']}>Личный проект</span>
          <h1>{template.data ? moduleTitle(template.data) : 'Подготовка документа'}</h1>
          <p>
            Сохранено: {new Date(draft.updated_at).toLocaleString('ru-RU')} · версия{' '}
            {draft.revision}
          </p>
        </div>
        <TemplateBadge template={draft} />
      </div>
      <div className={templateStyles['template-panel']}>
        <DraftProgress draft={draft} />
        <p className={ui['muted']}>
          {progress(draft).total
            ? 'Количество шагов меняется в зависимости от ответов.'
            : 'Проверьте применимые реквизиты.'}
        </p>
      </div>
      {saved && (
        <p role="status" className={templateStyles['template-success']}>
          {saved}
        </p>
      )}
      {query.isError && <ErrorNotice error={query.error} onRetry={() => void query.refetch()} />}
      {busy && (
        <Loading
          label={generate.isPending ? 'Проверяем данные и формируем DOCX…' : 'Обновляем данные…'}
        />
      )}
      {current ? (
        <>
          <FieldEditor
            key={current.key}
            field={current}
            draft={draft}
            editing={editing}
            onSaved={onSaved}
            onConflict={() => setSelected(current.key)}
            onCancel={() => {
              setReview(true);
              setSelected(null);
            }}
            reload={async () => {
              const result = await query.refetch();
              if (result.error) throw result.error;
              return result.data!;
            }}
          />
          {previous && (
            <button className={ui['text-button']} onClick={() => edit(previous.key)}>
              ← Изменить предыдущий ответ: {previous.label}
            </button>
          )}
        </>
      ) : (
        <>
          <div className={templateStyles['template-panel']}>
            <h2>Проверка проекта документа</h2>
            <p>
              Данные ниже загружены из сохранённого черновика. Проверьте реквизиты перед
              формированием проекта.
            </p>
            <div className={templateStyles['template-actions']}>
              <button
                className={cx(ui['button'], ui['secondary'])}
                disabled={busy}
                onClick={() => validate.mutate()}
              >
                Проверить данные
              </button>
              {report.next_question && (
                <button
                  className={cx(ui['button'], ui['secondary'])}
                  disabled={busy}
                  onClick={() => {
                    setSelected(null);
                    setReview(false);
                  }}
                >
                  Продолжить вопросы
                </button>
              )}
              <button
                className={cx(ui['button'], ui['primary'])}
                disabled={
                  busy ||
                  !report.ready ||
                  report.checks?.some((c) => c.category === 'blocking' && c.result === 'failed')
                }
                onClick={() => {
                  if (generateLock.current) return;
                  generateLock.current = true;
                  generate.mutate(undefined, {
                    onSettled: () => {
                      generateLock.current = false;
                    },
                  });
                }}
              >
                Сформировать проект DOCX
              </button>
            </div>
            <p className={ui['muted']}>
              Повторная генерация той же сохранённой версии возвращает тот же результат.
            </p>
          </div>
          {validate.isError && (
            <ErrorNotice error={validate.error} onRetry={() => validate.mutate()} />
          )}
          {generate.isError && (
            <ErrorNotice
              error={generate.error}
              message={
                generate.error instanceof ApiError && generate.error.status === 409
                  ? 'Черновик изменён в другом окне. Получите актуальные ответы и проверьте их перед повторной генерацией.'
                  : undefined
              }
              onRetry={
                generate.error instanceof ApiError && generate.error.status === 409
                  ? () => {
                      void query.refetch();
                      generate.reset();
                    }
                  : undefined
              }
            />
          )}
          <ValidationReport
            report={report}
            fields={draft.fields}
            profile={draft.profile}
            onEdit={edit}
          />
          <ApprovalRoute report={report} />
          <section className={templateStyles['template-panel']}>
            <h2>Данные документа</h2>
            <p className={ui['muted']}>
              Необязательные подтверждения сохраняются в черновике. Они не заменяют проверку
              человеком.
            </p>
            <div className={styles['template-review-fields']}>
              {fields.map((field) => (
                <article key={field.key}>
                  <div className={templateStyles['template-row-heading']}>
                    <h3>
                      {field.label}
                      {(report.required_fields?.includes(field.key) ?? field.required) ? ' *' : ''}
                    </h3>
                    <button
                      className={ui['text-button']}
                      disabled={busy}
                      onClick={() => edit(field.key)}
                    >
                      Изменить
                    </button>
                  </div>
                  <p className={templateStyles['template-value']}>
                    {valueText(field, draft.values[field.key])}
                  </p>
                </article>
              ))}
            </div>
          </section>
        </>
      )}
    </div>
  );
}
