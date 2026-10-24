import { AlertCircle, LoaderCircle } from 'lucide-react';
import { errorMessage } from '../../api/client';

export function Loading({ label = 'Загрузка…' }: { label?: string }) {
  return (
    <div className="loading" role="status">
      <LoaderCircle size={19} className="animate-spin" />
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
    <div className="error-notice" role="alert">
      <AlertCircle size={18} />
      <div>
        <p>{message ?? errorMessage(error)}</p>
        {onRetry && (
          <button className="text-button" onClick={onRetry}>
            Попробовать ещё раз
          </button>
        )}
      </div>
    </div>
  );
}
