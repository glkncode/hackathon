import { cx } from '../../../shared/lib/cx';
import type { DocumentTemplate } from '../types/templates';
import styles from './TemplateBadge.module.css';

export function TemplateBadge({
  template,
}: {
  template: Pick<DocumentTemplate, 'schema_version' | 'profile' | 'support_status'>;
}) {
  return (
    <span
      className={cx(styles['template-badge'], template.schema_version === 2 ? styles['v2'] : '')}
    >
      {template.support_status === 'needs_configuration'
        ? 'Требует настройки'
        : template.schema_version === 2
          ? `Проверенный v2 · ${template.profile?.revision ?? '2'}`
          : 'Legacy v1 · старый режим'}
    </span>
  );
}
