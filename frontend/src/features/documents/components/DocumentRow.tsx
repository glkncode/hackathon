import { useMutation, useQuery } from '@tanstack/react-query';
import { Download, FileText } from 'lucide-react';
import { cx } from '../../../shared/lib/cx';
import { saveDownload } from '../../../shared/lib/download';
import { ErrorNotice } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { documentsApi } from '../api/documents';
import { documentsKey } from '../hooks/keys';
import type { DocumentRecord, DocumentVersion } from '../types/documents';
import styles from './DocumentRow.module.css';

const statusLabels: Record<DocumentVersion['status'], string> = {
  uploaded: 'Загружен',
  processing: 'Обрабатывается',
  ready: 'Готов к поиску',
  failed: 'Ошибка обработки',
};
const dateFormat = new Intl.DateTimeFormat('ru-RU', {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
});

export function DocumentRow({ document }: { document: DocumentRecord }) {
  // List intentionally omits versions. A document without an active version can
  // return 404 from GET /documents/{id}, so use version history in that case.
  const metadata = useQuery({
    queryKey: [...documentsKey, 'metadata', document.id, document.active_version_id],
    queryFn: async ({ signal }) => {
      if (document.active_version_id) {
        const detail = await documentsApi.get(document.id, signal);
        return { active: detail.active_version, latest: detail.latest_version };
      }
      const versions = await documentsApi.versions(document.id, signal);
      return { active: null, latest: versions.items[0] ?? null };
    },
  });
  const download = useMutation({
    mutationFn: async () => saveDownload(await documentsApi.download(document.id), document.title),
  });
  return (
    <>
      <tr>
        <td>
          <div className={styles['document-name']}>
            <span className={styles['document-file-icon']}>
              <FileText size={20} />
            </span>
            <div>
              <strong>{document.title}</strong>
              {metadata.data?.latest && <span>{metadata.data.latest.original_filename}</span>}
            </div>
          </div>
          {metadata.isError && (
            <ErrorNotice error={metadata.error} onRetry={() => void metadata.refetch()} />
          )}
          {download.isError && <ErrorNotice error={download.error} />}
        </td>
        <td>
          {metadata.isPending ? (
            <span className={styles['skeleton']}>Загрузка…</span>
          ) : metadata.data?.active ? (
            <span className={styles['version-number']}>Версия {metadata.data.active.version_number}</span>
          ) : (
            <span className={ui['muted']}>
              {metadata.isError ? 'Недоступна' : 'Не опубликована'}
            </span>
          )}
        </td>
        <td>
          {metadata.data?.latest ? (
            <div className={styles['latest-version']}>
              <span>Версия {metadata.data.latest.version_number}</span>
              <span className={cx(styles['status-badge'], styles[metadata.data.latest.status])}>
                {statusLabels[metadata.data.latest.status]}
              </span>
            </div>
          ) : (
            <span className={ui['muted']}>{metadata.isPending ? 'Загрузка…' : '—'}</span>
          )}
        </td>
        <td className={styles['date-cell']}>
          <time dateTime={document.created_at}>
            {dateFormat.format(new Date(document.created_at))}
          </time>
        </td>
        <td>
          <button
            className={ui['icon-button']}
            aria-label={`Скачать документ «${document.title}»`}
            disabled={!document.active_version_id || download.isPending || metadata.isError}
            onClick={() => download.mutate()}
          >
            <Download size={18} />
          </button>
        </td>
      </tr>
    </>
  );
}
