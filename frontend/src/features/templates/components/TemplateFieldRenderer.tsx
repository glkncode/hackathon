import { cx } from '../../../shared/lib/cx';
import ui from '../../../shared/ui/ui.module.css';
import { choiceLabel } from '../lib/templateDisplay';
import templateStyles from '../styles/templates.module.css';
import type { FieldValue, TemplateField } from '../types/templates';
import styles from './TemplateFieldRenderer.module.css';

export function emptyValue(field: TemplateField): FieldValue {
  return ['repeater', 'person_list', 'text_list'].includes(field.type) ? [] : '';
}
export function TemplateFieldRenderer({
  field,
  value,
  onChange,
  required,
  disabled = false,
  error,
}: {
  field: TemplateField;
  value: FieldValue;
  onChange: (value: FieldValue) => void;
  required: boolean;
  disabled?: boolean;
  error?: string;
}) {
  const id = `field-${field.key}`;
  const common = {
    id,
    disabled,
    required,
    'aria-invalid': !!error,
    'aria-describedby': `${id}-hint${error ? ` ${id}-error` : ''}`,
    maxLength: field.max_length || 8000,
  };
  const rows = Array.isArray(value) ? value : [];
  const move = (index: number, delta: number) => {
    const next = [...rows];
    [next[index], next[index + delta]] = [next[index + delta]!, next[index]!];
    onChange(next as FieldValue);
  };
  return (
    <div className={styles['template-field']}>
      <label htmlFor={id}>
        {field.label}
        {required ? ' *' : ' (необязательно)'}
      </label>
      <p id={`${id}-hint`} className={ui['muted']}>
        {field.description ||
          (field.type === 'date'
            ? 'Выберите календарную дату.'
            : 'Ответ сохранится после нажатия кнопки «Сохранить».')}
      </p>
      {field.type === 'choice' ? (
        <select
          {...common}
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">Выберите вариант</option>
          {field.options?.map((option) => (
            <option key={option} value={option}>
              {choiceLabel(option)}
            </option>
          ))}
        </select>
      ) : field.type === 'multiline_text' ? (
        <textarea
          {...common}
          rows={5}
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : ['person_list', 'text_list', 'repeater'].includes(field.type) ? (
        <fieldset disabled={disabled} id={id} aria-describedby={`${id}-hint`}>
          <legend className={ui['sr-only']}>{field.label}</legend>
          <p className={ui['muted']}>
            {rows.length} строк{field.minimum_items ? ` · минимум ${field.minimum_items}` : ''} ·
            максимум {field.maximum_items ?? (field.type === 'person_list' ? 50 : 30)}
          </p>
          {rows.map((row, index) => (
            <div className={styles['template-row']} key={index}>
              <div className={templateStyles['template-row-heading']}>
                <strong>
                  {field.type === 'repeater' || field.type === 'person_list'
                    ? 'Участник'
                    : 'Строка'}{' '}
                  {index + 1}
                </strong>
                <div className={templateStyles['template-actions']}>
                  <button
                    type="button"
                    className={ui['text-button']}
                    disabled={index === 0}
                    aria-label={`Поднять строку ${index + 1}`}
                    onClick={() => move(index, -1)}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className={ui['text-button']}
                    disabled={index === rows.length - 1}
                    aria-label={`Опустить строку ${index + 1}`}
                    onClick={() => move(index, 1)}
                  >
                    ↓
                  </button>
                  <button
                    type="button"
                    className={ui['text-button']}
                    onClick={() => onChange(rows.filter((_, i) => i !== index) as FieldValue)}
                  >
                    Удалить строку {index + 1}
                  </button>
                </div>
              </div>
              {field.type === 'repeater' ? (
                field.item_fields?.map((item) => (
                  <label key={item.key} className={styles['template-item-label']}>
                    {item.label}
                    {item.required ? ' *' : ''}
                    <input
                      aria-label={`${item.label}, строка ${index + 1}`}
                      required={item.required}
                      maxLength={item.max_length ?? 1000}
                      value={typeof row === 'object' ? (row[item.key] ?? '') : ''}
                      onChange={(e) =>
                        onChange(
                          rows.map((current, i) =>
                            i === index
                              ? {
                                  ...(typeof current === 'object' ? current : {}),
                                  [item.key]: e.target.value,
                                }
                              : current,
                          ) as FieldValue,
                        )
                      }
                    />
                  </label>
                ))
              ) : (
                <input
                  aria-label={`${field.label}, строка ${index + 1}`}
                  required
                  maxLength={field.max_length}
                  value={typeof row === 'string' ? row : ''}
                  onChange={(e) =>
                    onChange(
                      rows.map((current, i) =>
                        i === index ? e.target.value : current,
                      ) as FieldValue,
                    )
                  }
                />
              )}
            </div>
          ))}
          <button
            type="button"
            className={cx(ui['button'], ui['secondary'])}
            disabled={
              rows.length >= (field.maximum_items ?? (field.type === 'person_list' ? 50 : 30))
            }
            onClick={() =>
              onChange([
                ...rows,
                field.type === 'repeater'
                  ? Object.fromEntries((field.item_fields ?? []).map((item) => [item.key, '']))
                  : '',
              ] as FieldValue)
            }
          >
            Добавить{' '}
            {field.type === 'repeater' || field.type === 'person_list' ? 'участника' : 'строку'}
          </button>
        </fieldset>
      ) : (
        <input
          {...common}
          type={field.type === 'date' ? 'date' : 'text'}
          value={typeof value === 'string' ? value : ''}
          onInput={field.type === 'date' ? (e) => onChange(e.currentTarget.value) : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {error && (
        <p id={`${id}-error`} className={styles['template-field-error']} role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
