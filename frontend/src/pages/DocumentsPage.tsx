import { useRef, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  Download,
  FileText,
  RefreshCw,
  Upload,
  X,
} from 'lucide-react';
import { documentsApi } from '../api/documents';
import { useAuth } from '../features/auth/useAuth';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import { saveDownload } from '../lib/download';
import type { DocumentRecord, DocumentVersion } from '../types/api';

const documentsKey = ['documents'] as const;
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

export function DocumentsPage() {
  const { data: user } = useAuth();
  const client = useQueryClient();
  const [offset, setOffset] = useState(0);
  const documents = useQuery({
    queryKey: [...documentsKey, 'list', offset],
    queryFn: ({ signal }) => documentsApi.list(offset, signal),
  });
  return (
    <section className="documents-page">
      <div className="page-heading">
        <div>
          <span className="eyebrow">ДОКУМЕНТЫ ОРГАНИЗАЦИИ</span>
          <h1>База знаний</h1>
          <p>Инструкции и регламенты, на которые опирается помощник.</p>
        </div>
        <button
          className="button secondary"
          onClick={() => void client.invalidateQueries({ queryKey: documentsKey })}
          disabled={documents.isFetching}
        >
          <RefreshCw size={16} className={documents.isFetching ? 'animate-spin' : ''} />
          Обновить
        </button>
      </div>
      {user?.role === 'admin' && <UploadDocument />}
      <div className="documents-panel">
        <div className="panel-heading">
          <h2>
            <BookOpen size={18} />
            Доступные документы
          </h2>
          {documents.data && <span className="count-badge">{documents.data.total}</span>}
        </div>
        {documents.isPending && <Loading label="Загружаем документы…" />}
        {documents.isError && (
          <ErrorNotice error={documents.error} onRetry={() => void documents.refetch()} />
        )}
        {documents.data?.items.length === 0 && (
          <div className="empty-documents">
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
          <div className="table-scroll">
            <table className="documents-table">
              <thead>
                <tr>
                  <th scope="col">Название документа</th>
                  <th scope="col">Активная версия</th>
                  <th scope="col">Последняя версия</th>
                  <th scope="col">Создан</th>
                  <th scope="col">
                    <span className="sr-only">Действия</span>
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
          <div className="pagination">
            <span>Страница {Math.floor(offset / 20) + 1}</span>
            <div>
              <button
                className="icon-button"
                aria-label="Предыдущая страница"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(offset - 20, 0))}
              >
                <ChevronLeft size={18} />
              </button>
              <button
                className="icon-button"
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
      <p className="documents-footnote">
        Доступ к документам определяется вашей ролью и правами в организации. В поиске участвуют
        только активные версии со статусом «Готов к поиску».
      </p>
    </section>
  );
}

function DocumentRow({ document }: { document: DocumentRecord }) {
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
          <div className="document-name">
            <span className="document-file-icon">
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
            <span className="skeleton">Загрузка…</span>
          ) : metadata.data?.active ? (
            <span className="version-number">v{metadata.data.active.version_number}</span>
          ) : (
            <span className="muted">{metadata.isError ? 'Недоступна' : 'Не опубликована'}</span>
          )}
        </td>
        <td>
          {metadata.data?.latest ? (
            <div className="latest-version">
              <span>v{metadata.data.latest.version_number}</span>
              <span className={`status-badge ${metadata.data.latest.status}`}>
                {statusLabels[metadata.data.latest.status]}
              </span>
            </div>
          ) : (
            <span className="muted">{metadata.isPending ? 'Загрузка…' : '—'}</span>
          )}
        </td>
        <td className="date-cell">
          <time dateTime={document.created_at}>
            {dateFormat.format(new Date(document.created_at))}
          </time>
        </td>
        <td>
          <button
            className="icon-button download-button"
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

function UploadDocument() {
  const [title, setTitle] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [validation, setValidation] = useState('');
  const [expanded, setExpanded] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const client = useQueryClient();
  const upload = useMutation({
    mutationFn: () => documentsApi.upload(title.trim(), file!),
    onSuccess: async () => {
      setTitle('');
      setFile(null);
      if (inputRef.current) inputRef.current.value = '';
      await client.invalidateQueries({ queryKey: documentsKey });
    },
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    if (upload.isPending) return;
    if (!file || !/\.(pdf|docx|txt)$/i.test(file.name)) {
      setValidation('Выберите файл PDF, DOCX или TXT.');
      return;
    }
    if (!file.size) {
      setValidation('Файл пуст. Выберите другой документ.');
      return;
    }
    if (
      !title.trim() ||
      Array.from(title.trim()).length > 255 ||
      Array.from(title).some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
    ) {
      setValidation('Введите название до 255 символов без управляющих символов.');
      return;
    }
    setValidation('');
    upload.mutate();
  }
  return (
    <div className="upload-panel">
      <div className="upload-heading">
        <span className="upload-icon">
          <Upload size={21} />
        </span>
        <div>
          <h2>Пополните базу знаний</h2>
          <p>Загрузите документ. После обработки он станет доступен для поиска.</p>
        </div>
        <button
          className="button secondary"
          aria-expanded={expanded}
          aria-controls="upload-form"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? (
            <>
              <X size={16} />
              Закрыть
            </>
          ) : (
            <>
              <Upload size={16} />
              Загрузить документ
            </>
          )}
        </button>
      </div>
      {expanded && (
        <form id="upload-form" className="upload-form" onSubmit={submit}>
          <div className="field">
            <label htmlFor="document-title">Название документа</label>
            <input
              id="document-title"
              value={title}
              onChange={(event) => {
                setTitle(event.target.value);
                upload.reset();
              }}
              placeholder="Например, Инструкция по документообороту"
              required
              disabled={upload.isPending}
            />
          </div>
          <div className="field">
            <label htmlFor="document-file">
              Файл <span className="muted">PDF, DOCX или TXT</span>
            </label>
            <input
              ref={inputRef}
              id="document-file"
              type="file"
              accept=".pdf,.docx,.txt"
              onChange={(event) => {
                setFile(event.target.files?.[0] ?? null);
                setValidation('');
                upload.reset();
              }}
              required
              disabled={upload.isPending}
            />
          </div>
          <button
            className="button primary"
            disabled={upload.isPending || !file || !title.trim()}
            type="submit"
          >
            {upload.isPending ? (
              <Loading label="Загружаем…" />
            ) : (
              <>
                <Upload size={16} />
                Загрузить
              </>
            )}
          </button>
          {validation && <ErrorNotice message={validation} />}
          {upload.isError && <ErrorNotice error={upload.error} />}
          {upload.isSuccess && (
            <p className="success-notice" role="status">
              Документ загружен. Список обновлён; обработка может занять некоторое время.
            </p>
          )}
        </form>
      )}
    </div>
  );
}
