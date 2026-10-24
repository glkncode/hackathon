import { useMutation } from '@tanstack/react-query';
import { ChevronDown, Download, FileText, LoaderCircle } from 'lucide-react';
import { useState } from 'react';
import { downloadDocumentVersion } from '../../../shared/api/documentFiles';
import { cx } from '../../../shared/lib/cx';
import { saveDownload } from '../../../shared/lib/download';
import { ErrorNotice } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import type { Source } from '../types/chat';
import styles from './SourceCard.module.css';

export function SourceCard({ source, index }: { source: Source; index: number }) {
  const [expanded, setExpanded] = useState(false);
  const download = useMutation({
    mutationFn: async () =>
      saveDownload(await downloadDocumentVersion(source), source.document_title),
  });
  const metadata = [
    `Версия ${source.version_number}`,
    source.section_number ? `Пункт ${source.section_number}` : null,
    source.section_title,
    source.page_number != null ? `Страница ${source.page_number}` : null,
  ]
    .filter(Boolean)
    .join(' · ');
  const long = source.content.length > 480;

  return (
    <article className={styles['source-card']}>
      <div className={styles['source-header']}>
        <span className={styles['source-icon']}>
          <FileText size={19} />
        </span>
        <div>
          <h3>{source.document_title}</h3>
          <p>{metadata}</p>
        </div>
        <span className={styles['source-number']}>{String(index + 1).padStart(2, '0')}</span>
      </div>
      <blockquote
        className={
          long && !expanded
            ? cx(styles['source-excerpt'], styles['collapsed'])
            : styles['source-excerpt']
        }
      >
        {source.content}
      </blockquote>
      <div className={styles['source-actions']}>
        {long && (
          <button
            className={ui['text-button']}
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'Свернуть' : 'Показать фрагмент целиком'}
            <ChevronDown size={14} className={expanded ? 'rotate-180' : ''} />
          </button>
        )}
        <button
          className={cx(ui['text-button'], styles['source-download'])}
          disabled={download.isPending}
          onClick={() => download.mutate()}
        >
          {download.isPending ? (
            <LoaderCircle size={14} className={'animate-spin'} />
          ) : (
            <Download size={14} />
          )}
          Скачать версию
        </button>
      </div>
      {download.isError && <ErrorNotice error={download.error} />}
    </article>
  );
}
