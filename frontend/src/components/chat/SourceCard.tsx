import { useState } from 'react';
import { ChevronDown, Download, FileText, LoaderCircle } from 'lucide-react';
import { useMutation } from '@tanstack/react-query';
import type { Source } from '../../types/api';
import { documentsApi } from '../../api/documents';
import { saveDownload } from '../../lib/download';
import { ErrorNotice } from '../ui/Feedback';

export function SourceCard({ source, index }: { source: Source; index: number }) {
  const [expanded, setExpanded] = useState(false);
  const download = useMutation({
    mutationFn: async () =>
      saveDownload(await documentsApi.downloadSource(source), source.document_title),
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
    <article className="source-card">
      <div className="source-header">
        <span className="source-icon">
          <FileText size={19} />
        </span>
        <div>
          <h3>{source.document_title}</h3>
          <p>{metadata}</p>
        </div>
        <span className="source-number">{String(index + 1).padStart(2, '0')}</span>
      </div>
      <blockquote className={long && !expanded ? 'source-excerpt collapsed' : 'source-excerpt'}>
        {source.content}
      </blockquote>
      <div className="source-actions">
        {long && (
          <button
            className="text-button"
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'Свернуть' : 'Показать фрагмент целиком'}
            <ChevronDown size={14} className={expanded ? 'rotate-180' : ''} />
          </button>
        )}
        <button
          className="text-button source-download"
          disabled={download.isPending}
          onClick={() => download.mutate()}
        >
          {download.isPending ? (
            <LoaderCircle size={14} className="animate-spin" />
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
