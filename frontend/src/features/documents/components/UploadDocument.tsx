import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Upload, X } from 'lucide-react';
import { useRef, useState, type FormEvent } from 'react';
import { cx } from '../../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { documentsApi } from '../api/documents';
import { documentsKey } from '../hooks/keys';
import styles from './UploadDocument.module.css';

export function UploadDocument() {
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
    <div className={styles['upload-panel']}>
      <div className={styles['upload-heading']}>
        <span className={styles['upload-icon']}>
          <Upload size={21} />
        </span>
        <div>
          <h2>Пополните базу знаний</h2>
          <p>Загрузите документ. После обработки он станет доступен для поиска.</p>
        </div>
        <button
          className={cx(ui['button'], ui['secondary'])}
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
        <form id="upload-form" className={styles['upload-form']} onSubmit={submit}>
          <div className={ui['field']}>
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
          <div className={ui['field']}>
            <label htmlFor="document-file">
              Файл <span className={ui['muted']}>PDF, DOCX или TXT</span>
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
            className={cx(ui['button'], ui['primary'])}
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
            <p className={styles['success-notice']} role="status">
              Документ загружен. Список обновлён; обработка может занять некоторое время.
            </p>
          )}
        </form>
      )}
    </div>
  );
}
