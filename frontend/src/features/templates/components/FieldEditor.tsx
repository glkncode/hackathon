import { useMutation } from '@tanstack/react-query';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { ApiError } from '../../../shared/api/client';
import { cx } from '../../../shared/lib/cx';
import { ErrorNotice } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { templatesApi } from '../api/templates';
import { valueText } from '../lib/templateDisplay';
import templateStyles from '../styles/templates.module.css';
import type { FieldValue, TemplateDraft, TemplateField } from '../types/templates';
import { emptyValue, TemplateFieldRenderer } from './TemplateFieldRenderer';
import { SourceNote } from './ValidationReport';
import validationReportStyles from './ValidationReport.module.css';

export function FieldEditor({
  field,
  draft,
  editing,
  onSaved,
  onCancel,
  onConflict,
  reload,
}: {
  field: TemplateField;
  draft: TemplateDraft;
  editing: boolean;
  onSaved: (draft: TemplateDraft) => void;
  onCancel: () => void;
  onConflict: () => void;
  reload: () => Promise<TemplateDraft>;
}) {
  const [value, setValue] = useState<FieldValue>(draft.values[field.key] ?? emptyValue(field));
  const [conflict, setConflict] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [expectedRevision, setExpectedRevision] = useState(draft.revision);
  const [reloadError, setReloadError] = useState<unknown>();
  const lock = useRef(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, [field.key]);
  const visible =
    !draft.validation?.visible_fields || draft.validation.visible_fields.includes(field.key);
  const required = draft.validation?.required_fields?.includes(field.key) ?? field.required;
  const save = useMutation({
    mutationFn: () =>
      editing
        ? templatesApi.update(draft.id, expectedRevision, { [field.key]: value })
        : templatesApi.answer(draft.id, expectedRevision, field.key, value),
    onSuccess: onSaved,
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
        onConflict();
      }
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (lock.current || conflict || !visible) return;
    lock.current = true;
    save.mutate(undefined, {
      onSettled: () => {
        lock.current = false;
      },
    });
  };
  if (save.error instanceof ApiError && [401, 403, 404].includes(save.error.status))
    return (
      <div className={templateStyles['template-panel']}>
        <ErrorNotice error={save.error} />
        <Link to="/templates">К доступным шаблонам</Link>
      </div>
    );
  return (
    <form className={templateStyles['template-panel']} onSubmit={submit}>
      <span className={ui['eyebrow']}>
        {editing
          ? 'Изменение ответа'
          : `Шаг ${Math.min(draft.required_field_count, draft.completed_field_count + 1)} из ${draft.required_field_count}`}
      </span>
      <h2 ref={headingRef} tabIndex={-1}>
        {field.question.replace(/\?+$/, '?')}
      </h2>
      <TemplateFieldRenderer
        field={field}
        value={value}
        onChange={setValue}
        required={required}
        disabled={save.isPending || refreshing}
        error={
          save.error instanceof ApiError && [400, 422].includes(save.error.status)
            ? field.type === 'date'
              ? 'Проверьте дату: требуется существующая календарная дата в формате ГГГГ-ММ-ДД.'
              : 'Ответ не принят. Проверьте заполнение, число строк и допустимую длину.'
            : undefined
        }
      />
      <SourceNote source={field.source} />
      {save.isError && !(save.error instanceof ApiError && save.error.status === 409) && (
        <ErrorNotice
          error={save.error}
          message={
            save.error instanceof ApiError && save.error.status === 400
              ? 'Ответ не принят. Проверьте формат даты, заполнение строк и допустимую длину. Ввод сохранён на экране; исправьте и повторите.'
              : undefined
          }
        />
      )}
      {conflict && (
        <div
          role="alert"
          className={cx(
            validationReportStyles['template-check'],
            validationReportStyles['warning'],
          )}
        >
          <strong>Черновик изменён в другом окне.</strong>
          <p>
            Ваш ввод сохранён на экране. Получите актуальную версию, сравните сохранённый ответ и
            повторно нажмите «Сохранить», если хотите применить свой ввод.
          </p>
          <button
            type="button"
            className={cx(ui['button'], ui['secondary'])}
            disabled={refreshing}
            onClick={async () => {
              setRefreshing(true);
              setReloadError(undefined);
              try {
                const latest = await reload();
                setExpectedRevision(latest.revision);
                setConflict(false);
              } catch (error) {
                setReloadError(error);
              } finally {
                setRefreshing(false);
              }
            }}
          >
            Получить актуальный черновик
          </button>
        </div>
      )}
      {reloadError !== undefined && <ErrorNotice error={reloadError} />}
      {(editing || conflict || expectedRevision !== draft.revision) && (
        <details open={conflict || undefined}>
          <summary>Сейчас сохранено · версия {draft.revision}</summary>
          <p className={templateStyles['template-value']}>
            {valueText(field, draft.values[field.key])}
          </p>
        </details>
      )}
      {!visible && (
        <p role="alert" className={templateStyles['template-warning']}>
          После обновления это поле больше не применяется. Ввод остаётся на экране; вернитесь к
          проверке данных.
        </p>
      )}
      <div className={templateStyles['template-actions']}>
        <button
          className={cx(ui['button'], ui['primary'])}
          disabled={save.isPending || conflict || refreshing || !visible}
        >
          {save.isPending
            ? 'Сохраняем…'
            : save.isError && !(save.error instanceof ApiError && save.error.status === 409)
              ? 'Повторить сохранение'
              : editing
                ? 'Сохранить изменение'
                : 'Сохранить и продолжить'}
        </button>
        <button
          type="button"
          className={cx(ui['button'], ui['secondary'])}
          disabled={save.isPending || refreshing}
          onClick={onCancel}
        >
          К проверке данных
        </button>
      </div>
      <p className={ui['muted']}>
        Для продолжения позже сохраните ответ, затем откройте этот проект в разделе «Мои черновики».
      </p>
    </form>
  );
}
