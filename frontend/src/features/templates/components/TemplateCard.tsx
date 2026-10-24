import { FilePlus2, Files } from 'lucide-react';
import { cx } from '../../../shared/lib/cx';
import ui from '../../../shared/ui/ui.module.css';
import { documentKind, moduleTitle } from '../lib/templateDisplay';
import templateStyles from '../styles/templates.module.css';
import type { DocumentTemplate } from '../types/templates';
import { TemplateBadge } from './TemplateBadge';
import styles from './TemplateCard.module.css';

export function TemplateCard({
  template,
  pending,
  onCreate,
}: {
  template: DocumentTemplate;
  pending: boolean;
  onCreate: () => void;
}) {
  const supported = template.enabled && template.support_status !== 'needs_configuration';
  return (
    <article className={styles['template-card']}>
      <Files size={23} />
      <TemplateBadge template={template} />
      <h3>{moduleTitle(template)}</h3>
      <p className={ui['muted']}>
        {documentKind(template)} · {template.name}
      </p>
      <p>
        {template.description ||
          (template.schema_version === 2
            ? 'Подготовка проекта по проверенному модулю со структурной обработкой DOCX.'
            : 'Заполнение по старой схеме. Структурная очистка v2 не предусмотрена.')}
      </p>
      {!supported && (
        <p className={templateStyles['template-warning']}>
          {template.support_status === 'needs_configuration'
            ? 'Нужен проверенный профиль, связанный с исходным DOCX. Обратитесь к администратору.'
            : 'Создание временно недоступно.'}
        </p>
      )}
      <button
        className={cx(ui['button'], ui['primary'])}
        disabled={!supported || pending}
        onClick={onCreate}
      >
        <FilePlus2 size={17} />
        {pending ? 'Создаём…' : 'Создать документ'}
      </button>
    </article>
  );
}
