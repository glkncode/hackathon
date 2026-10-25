import { cx } from '../../../shared/lib/cx';
import ui from '../../../shared/ui/ui.module.css';
import { ruleFields } from '../lib/templateDisplay';
import templateStyles from '../styles/templates.module.css';
import type {
  RuleSource,
  TemplateField,
  TemplateProfile,
  TemplateValidation,
} from '../types/templates';
import styles from './ValidationReport.module.css';

export function SourceNote({ source }: { source?: RuleSource }) {
  return source?.name ? (
    <p className={styles['template-source']}>
      Источник: {source.name}
      {source.section && ` · ${source.section}`}
      {source.version && (
        <span title={source.version}>
          {' '}
          · версия{' '}
          {source.version.startsWith('sha256:')
            ? source.version.slice(0, 19) + '…'
            : source.version}
        </span>
      )}
    </p>
  ) : null;
}
const categories = {
  blocking: 'Блокирующие проверки',
  warning: 'Предупреждения',
  manual_review: 'Необходима ручная проверка',
  info: 'Пояснения',
};
const results: Record<string, string> = {
  passed: 'Условие выполнено',
  failed: 'Ошибка',
  not_applicable: 'Не применяется',
  review_required: 'Необходима проверка человеком',
  attention: 'Обратите внимание',
  user_confirmed: 'Подтверждено пользователем; требует независимой проверки',
};
export function ValidationReport({
  report,
  fields = [],
  profile,
  onEdit,
}: {
  report: TemplateValidation;
  fields?: TemplateField[];
  profile?: TemplateProfile;
  onEdit?: (key: string) => void;
}) {
  return (
    <section className={styles['template-report']} aria-label="Результаты проверки">
      <h2>Результаты проверки</h2>
      <p
        className={
          report.ready ? templateStyles['template-success'] : templateStyles['template-warning']
        }
      >
        {report.ready
          ? 'Можно сформировать DOCX-проект.'
          : 'Для генерации нужно заполнить поля и устранить блокирующие ошибки.'}
      </p>
      <p className={ui['muted']}>
        Готовность к генерации не означает согласование, подписание или официальную регистрацию.
      </p>
      {!!report.missing_fields?.length && (
        <div className={cx(styles['template-check'], styles['blocking'])}>
          <h3>Обязательные поля</h3>
          {report.missing_fields.map((field) => (
            <p key={field.key}>
              <button
                className={ui['text-button']}
                disabled={!onEdit}
                onClick={() => onEdit?.(field.key)}
              >
                {field.label} — заполнить
              </button>
            </p>
          ))}
        </div>
      )}
      {!!report.invalid_fields?.length && (
        <div className={cx(styles['template-check'], styles['blocking'])}>
          <h3>Проверьте значения</h3>
          {report.invalid_fields.map((field) => (
            <p key={field.key}>
              <button
                className={ui['text-button']}
                disabled={!onEdit}
                onClick={() => onEdit?.(field.key)}
              >
                {fields.find((f) => f.key === field.key)?.label ?? 'Поле документа'} — исправить
              </button>
            </p>
          ))}
        </div>
      )}
      {(Object.keys(categories) as (keyof typeof categories)[]).map((category) => {
        const checks = report.checks?.filter((check) => check.category === category) ?? [];
        const quiet = checks.filter(
          (check) =>
            category !== 'manual_review' && ['passed', 'not_applicable'].includes(check.result),
        );
        const renderCheck = (check: NonNullable<TemplateValidation['checks']>[number]) => {
          const related = check.rule_id.startsWith('field.')
            ? [check.rule_id.slice(6)]
            : ruleFields(profile, check.rule_id);
          const editable = fields.filter(
            (field) =>
              (!report.visible_fields || report.visible_fields.includes(field.key)) &&
              (!related.length || related.includes(field.key)),
          );
          return (
            <article
              key={check.rule_id}
              className={cx(
                styles['template-check'],
                styles[category],
                category === 'manual_review' ? '' : styles[check.result],
              )}
            >
              <strong>
                {category === 'manual_review' &&
                !['user_confirmed', 'not_applicable'].includes(check.result)
                  ? 'Необходима проверка человеком'
                  : (results[check.result] ?? 'Необходима проверка')}
              </strong>
              <p>{check.description}</p>
              <SourceNote source={check.source} />
              {category === 'blocking' && check.result === 'failed' && onEdit && (
                <div className={templateStyles['template-actions']}>
                  {editable.map((field) => (
                    <button
                      key={field.key}
                      className={ui['text-button']}
                      onClick={() => onEdit(field.key)}
                    >
                      Исправить: {field.label}
                    </button>
                  ))}
                </div>
              )}
            </article>
          );
        };
        return checks.length ? (
          <div className={styles['template-check-group']} key={category}>
            <h3>{categories[category]}</h3>
            {checks.filter((check) => !quiet.includes(check)).map(renderCheck)}
            {!!quiet.length && (
              <details>
                <summary>Успешные и неприменимые проверки ({quiet.length})</summary>
                <div className={styles['template-check-group']}>{quiet.map(renderCheck)}</div>
              </details>
            )}
          </div>
        ) : null;
      })}
    </section>
  );
}
export function ApprovalRoute({ report }: { report: TemplateValidation }) {
  const route = report.approval_route;
  if (!route) return null;
  return (
    <section className={cx(templateStyles['template-panel'], styles['template-route'])}>
      <span className={ui['eyebrow']}>Рекомендация</span>
      <h2>Рекомендуемый маршрут согласования</h2>
      <p>{route.note}</p>
      <h3>Требуется визирование</h3>
      <p>
        {route.required_departments.length
          ? route.required_departments.join(' · ')
          : 'Состав определяется по применимым условиям и компетенции подразделений.'}
      </p>
      {!!route.conditional_departments.length && (
        <>
          <h3>Условные согласования</h3>
          {route.conditional_departments.map((department, i) => (
            <div className={cx(styles['template-check'], styles['info'])} key={i}>
              <strong>{department.name}</strong>
              <p>{department.condition}</p>
              <p className={ui['muted']}>
                {department.applicable
                  ? 'Условие применимо — требуется проверка участия'
                  : 'Условие сейчас не применимо; это не запрет согласования'}
              </p>
              <SourceNote source={department.source} />
            </div>
          ))}
        </>
      )}
      {!!route.additional_departments.length && (
        <>
          <h3>Дополнительные подразделения</h3>
          <p>{route.additional_departments.join(' · ')}</p>
        </>
      )}
      <details>
        <summary>Основания из Матрицы</summary>
        {route.rows.map((row) => (
          <article key={row.row_id} className={cx(styles['template-check'], styles['info'])}>
            <strong>{row.description}</strong>
            <p>{row.applicable ? 'Применимо к этому проекту' : 'Сейчас не применяется'}</p>
            <SourceNote source={row.source} />
          </article>
        ))}
      </details>
      <p className={ui['muted']}>
        Может потребоваться дополнительное согласование. Итоговый состав участников необходимо
        проверить.
      </p>
    </section>
  );
}
