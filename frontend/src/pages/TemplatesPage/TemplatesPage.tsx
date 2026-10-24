import { Link } from 'react-router-dom';
import { useAuth } from '../../features/auth';
import { DraftCard, TemplateCard, useTemplateCatalogController } from '../../features/templates';
import templateStyles from '../../features/templates/styles/templates.module.css';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './TemplatesPage.module.css';

export function TemplatesPage() {
  const { catalog, drafts, createLock, create } = useTemplateCatalogController();
  const user = useAuth();
  return (
    <div className={templateStyles['templates-page']}>
      <div className={ui['page-heading']}>
        <div>
          <span className={ui['eyebrow']}>ПОДГОТОВКА ДОКУМЕНТОВ</span>
          <h1>Шаблоны</h1>
          <p>
            От проверенного модуля до проекта DOCX — с сохранением ответов и проверкой реквизитов.
          </p>
        </div>
        {user.data?.role === 'admin' && (
          <Link to="/templates/register" className={cx(ui['button'], ui['secondary'])}>
            Зарегистрировать шаблон
          </Link>
        )}
      </div>
      {create.isError && <ErrorNotice error={create.error} />}
      <section aria-label="Каталог шаблонов">
        <h2>Доступные шаблоны</h2>
        {catalog.isPending && <Loading />}
        {catalog.isError ? (
          <ErrorNotice error={catalog.error} onRetry={() => void catalog.refetch()} />
        ) : (
          <div className={styles['template-grid']}>
            {catalog.data?.pages
              .flatMap((page) => page.items ?? [])
              .map((template) => (
                <TemplateCard
                  key={template.id}
                  template={template}
                  pending={create.isPending}
                  onCreate={() => {
                    if (createLock.current) return;
                    createLock.current = true;
                    create.mutate(template.id, {
                      onSettled: () => {
                        createLock.current = false;
                      },
                    });
                  }}
                />
              ))}
          </div>
        )}
        {catalog.isSuccess && !catalog.data.pages.some((p) => p.items?.length) && (
          <p className={templateStyles['template-empty']}>
            Доступных шаблонов пока нет. Администратор может зарегистрировать проверенный модуль и
            предоставить доступ.
          </p>
        )}
        {catalog.hasNextPage && (
          <button
            className={cx(ui['button'], ui['secondary'])}
            disabled={catalog.isFetchingNextPage}
            onClick={() => void catalog.fetchNextPage()}
          >
            Показать ещё шаблоны
          </button>
        )}
      </section>
      <section aria-label="Мои черновики">
        <h2>Мои черновики</h2>
        {drafts.isPending && <Loading />}
        {drafts.isError ? (
          <ErrorNotice error={drafts.error} onRetry={() => void drafts.refetch()} />
        ) : (
          <div className={styles['template-grid']}>
            {drafts.data?.pages
              .flatMap((page) => page.items ?? [])
              .map((draft) => (
                <DraftCard key={draft.id} draft={draft} />
              ))}
          </div>
        )}
        {drafts.isSuccess && !drafts.data.pages.some((p) => p.items?.length) && (
          <p className={templateStyles['template-empty']}>
            Создайте первый проект. Ответы будут сохранены в вашем личном черновике.
          </p>
        )}
        {drafts.hasNextPage && (
          <button
            className={cx(ui['button'], ui['secondary'])}
            disabled={drafts.isFetchingNextPage}
            onClick={() => void drafts.fetchNextPage()}
          >
            Показать ещё черновики
          </button>
        )}
      </section>
    </div>
  );
}
