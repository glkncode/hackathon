import { AlertCircle, LoaderCircle } from 'lucide-react';
import { errorMessage } from '../api/client';
import ui from './ui.module.css';

export function Loading({ label = 'Загрузка…' }: { label?: string }) {
  return (
    <div className={ui['loading']} role="status">
      <LoaderCircle size={19} className={'animate-spin'} />
      <span>{label}</span>
    </div>
  );
}

export function ErrorNotice({
  error,
  message,
  onRetry,
}: {
  error?: unknown;
  message?: string;
  onRetry?: () => void;
}) {
  return (
    <div className={ui['error-notice']} role="alert">
      <AlertCircle size={18} />
      <div>
        <p>{message ?? errorMessage(error)}</p>
        {onRetry && (
          <button className={ui['text-button']} onClick={onRetry}>
            Попробовать ещё раз
          </button>
        )}
      </div>
    </div>
  );
}
