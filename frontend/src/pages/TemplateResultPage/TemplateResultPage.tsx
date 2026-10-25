import { Link, useParams } from 'react-router-dom';
import {
  ApprovalRoute,
  formatTemplateDate,
  moduleTitle,
  useTemplateResult,
  ValidationReport,
} from '../../features/templates';
import templateStyles from '../../features/templates/styles/templates.module.css';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './TemplateResultPage.module.css';

export function TemplateResultPage() {
  const { id = '' } = useParams();
  const { query, result, template, download } = useTemplateResult(id);
  if (query.isPending)
    return (
      <div className={templateStyles['templates-page']}>
        <Loading />
      </div>
    );
  if (query.isError || !result || template.isError)
    return (
      <div className={templateStyles['templates-page']}>
        <ErrorNotice
          error={query.error ?? template.error}
          onRetry={() => {
            void query.refetch();
            void template.refetch();
          }}
        />
        <Link to="/templates">К шаблонам</Link>
      </div>
    );
  const snapshot = result.generation_snapshot;
  return (
    <div className={templateStyles['templates-page']}>
      <Link to="/templates" className={ui['text-button']}>
        ← Шаблоны и мои черновики
      </Link>
      <div className={ui['page-heading']}>
        <div>
          <span className={ui['eyebrow']}>Документ готов к скачиванию</span>
          <h1>Проект документа сформирован</h1>
          <p>Для официального использования необходимы проверка, согласование и подписание.</p>
        </div>
      </div>
      <section className={templateStyles['template-panel']}>
        <h2>{result.filename}</h2>
        <dl className={styles['template-metadata']}>
          <dt>Исходный шаблон</dt>
          <dd>{template.data ? moduleTitle(template.data) : 'Загрузка…'}</dd>
          <dt>Создан</dt>
          <dd>{formatTemplateDate(result.created_at)}</dd>
          <dt>Версия черновика</dt>
          <dd>{result.draft_revision}</dd>
          <dt>Профиль</dt>
          <dd>
            {snapshot
              ? `v${snapshot.profile.profile_version} · ${snapshot.profile.revision}`
              : 'Первая версия'}
          </dd>
          <dt>Идентификатор результата</dt>
          <dd>{result.id}</dd>
        </dl>
        <div className={templateStyles['template-actions']}>
          <button
            className={cx(ui['button'], ui['primary'])}
            disabled={download.isPending || query.isFetching}
            onClick={() => download.mutate()}
          >
            {download.isPending ? 'Скачиваем…' : 'Скачать DOCX'}
          </button>
          <Link
            className={cx(ui['button'], ui['secondary'])}
            to={`/templates/drafts/${result.draft_id}`}
          >
            Вернуться к черновику
          </Link>
        </div>
        <p className={ui['muted']}>
          Сохраните ссылку на эту страницу для повторного скачивания. Результат хранит данные версии{' '}
          {result.draft_revision} и не меняется при последующем редактировании черновика.
        </p>
        {download.isError && (
          <ErrorNotice error={download.error} onRetry={() => download.mutate()} />
        )}
      </section>
      {snapshot && (
        <>
          <ValidationReport report={snapshot.validation} fields={snapshot.profile.fields} />
          <ApprovalRoute report={snapshot.validation} />
        </>
      )}
    </div>
  );
}
