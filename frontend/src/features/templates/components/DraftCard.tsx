import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ApiError } from '../../../shared/api/client';
import { cx } from '../../../shared/lib/cx';
import { ErrorNotice } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { templatesApi } from '../api/templates';
import { formatTemplateDate, moduleTitle } from '../lib/templateDisplay';
import type { TemplateDraft } from '../types/templates';
import { DraftProgress } from './DraftProgress';
import templateCardStyles from './TemplateCard.module.css';

export function DraftCard({ draft }: { draft: TemplateDraft }) {
  const template = useQuery({
    queryKey: ['templates', 'detail', draft.template_id],
    queryFn: ({ signal }) => templatesApi.get(draft.template_id, signal),
    gcTime: 0,
  });
  if (
    template.isError &&
    template.error instanceof ApiError &&
    [401, 403, 404].includes(template.error.status)
  )
    return null;
  if (template.isError)
    return (
      <article className={templateCardStyles['template-card']}>
        <ErrorNotice error={template.error} onRetry={() => void template.refetch()} />
      </article>
    );
  return (
    <article className={templateCardStyles['template-card']}>
      <h3>{template.data ? moduleTitle(template.data) : 'Сохранённый проект'}</h3>
      <p className={ui['muted']}>
        Создан: {formatTemplateDate(draft.created_at)}
        <br />
        Изменён: {formatTemplateDate(draft.updated_at)}
      </p>
      <DraftProgress draft={draft} />
      <p>
        {(draft.validation?.ready ?? draft.status === 'ready_to_generate')
          ? 'Данные заполнены · доступна проверка'
          : 'Черновик · продолжите подготовку'}
      </p>
      <Link className={cx(ui['button'], ui['secondary'])} to={`/templates/drafts/${draft.id}`}>
        Продолжить
      </Link>
    </article>
  );
}
