import { BookOpen, ChevronLeft, ChevronRight, FileText, RefreshCw } from 'lucide-react';
import { useAuth } from '../../features/auth';
import { DocumentRow, UploadDocument, useDocuments } from '../../features/documents';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './DocumentsPage.module.css';

export function DocumentsPage() {
  const { data: user } = useAuth();
  const { documents, offset, setOffset, refresh } = useDocuments();
  return (
    <section className={styles['documents-page']}>
      <div className={ui['page-heading']}>
        <div>
          <span className={ui['eyebrow']}>Документы организации</span>
          <h1>База знаний</h1>
          <p>Инструкции и регламенты, на которые опирается помощник.</p>
        </div>
        <button
          className={cx(ui['button'], ui['secondary'])}
          onClick={() => void refresh()}
          disabled={documents.isFetching}
        >
          <RefreshCw size={16} className={documents.isFetching ? 'animate-spin' : ''} />
          Обновить
        </button>
      </div>
      {user?.role === 'admin' && <UploadDocument />}
      <div className={styles['documents-panel']}>
        <div className={styles['panel-heading']}>
          <h2>
            <BookOpen size={18} />
            Доступные документы
          </h2>
          {documents.data && <span className={styles['count-badge']}>{documents.data.total}</span>}
        </div>
        {documents.isPending && <Loading label="Загружаем документы…" />}
        {documents.isError && (
          <ErrorNotice error={documents.error} onRetry={() => void documents.refetch()} />
        )}
        {documents.data?.items.length === 0 && (
          <div className={styles['empty-documents']}>
            <span>
              <FileText size={28} />
            </span>
            <h3>{offset ? 'На этой странице нет документов' : 'Пока нет доступных документов'}</h3>
            <p>
              {offset
                ? 'Вернитесь на предыдущую страницу.'
                : user?.role === 'admin'
                  ? 'Загрузите первый документ, чтобы пополнить базу знаний.'
                  : 'Обратитесь к администратору, чтобы получить доступ.'}
            </p>
          </div>
        )}
        {!!documents.data?.items.length && (
          <div className={styles['table-scroll']}>
            <table className={styles['documents-table']}>
              <thead>
                <tr>
                  <th scope="col">Название документа</th>
                  <th scope="col">Активная версия</th>
                  <th scope="col">Последняя версия</th>
                  <th scope="col">Создан</th>
                  <th scope="col">
                    <span className={ui['sr-only']}>Действия</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {documents.data.items.map((document) => (
                  <DocumentRow key={document.id} document={document} />
                ))}
              </tbody>
            </table>
          </div>
        )}
        {documents.data && (documents.data.total > 20 || offset > 0) && (
          <div className={styles['pagination']}>
            <span>Страница {Math.floor(offset / 20) + 1}</span>
            <div>
              <button
                className={ui['icon-button']}
                aria-label="Предыдущая страница"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(offset - 20, 0))}
              >
                <ChevronLeft size={18} />
              </button>
              <button
                className={ui['icon-button']}
                aria-label="Следующая страница"
                disabled={offset + 20 >= documents.data.total}
                onClick={() => setOffset(offset + 20)}
              >
                <ChevronRight size={18} />
              </button>
            </div>
          </div>
        )}
      </div>
      <p className={styles['documents-footnote']}>
        Доступ к документам определяется вашей ролью и правами в организации. В поиске участвуют
        только активные версии со статусом «Готов к поиску».
      </p>
    </section>
  );
}
