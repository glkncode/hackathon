import { ArrowUp, LoaderCircle } from 'lucide-react';
import { useEffect, useRef } from 'react';
import { cx } from '../../../shared/lib/cx';
import ui from '../../../shared/ui/ui.module.css';
import styles from './ChatInput.module.css';

export function ChatInput({
  value,
  onChange,
  onSend,
  pending,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  onSend: () => void;
  pending: boolean;
  disabled?: boolean;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const length = Array.from(value).length;
  const invalid = length > 500 || value.includes('\0');
  const canSend = value.trim().length > 0 && !pending && !disabled && !invalid;
  useEffect(() => {
    if (!ref.current) return;
    ref.current.style.height = 'auto';
    ref.current.style.height = `${Math.min(ref.current.scrollHeight, 180)}px`;
  }, [value]);
  useEffect(() => {
    if (!pending && !disabled) ref.current?.focus({ preventScroll: true });
  }, [pending, disabled]);

  return (
    <div>
      <form
        className={cx(styles['composer'], invalid ? styles['invalid'] : '')}
        onSubmit={(event) => {
          event.preventDefault();
          if (canSend) onSend();
        }}
      >
        <label className={ui['sr-only']} htmlFor="chat-message">
          Вопрос по документам
        </label>
        <textarea
          ref={ref}
          id="chat-message"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder="Спросите о регламенте или документе…"
          rows={1}
          disabled={pending || disabled}
          aria-describedby="input-hint input-status"
          aria-invalid={invalid || undefined}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault();
              if (canSend) onSend();
            }
          }}
        />
        <div className={styles['composer-bottom']}>
          <span id="input-status" role="status" className={invalid ? styles['input-invalid'] : ''}>
            {pending
              ? 'Обрабатываем сообщение…'
              : invalid
                ? 'До 500 символов, без нулевого символа'
                : ''}
          </span>
          <div className={styles['composer-controls']}>
            {length > 400 && <span className={styles['character-count']}>{length}/500</span>}
            <button
              className={styles['send-button']}
              type="submit"
              disabled={!canSend}
              aria-label={pending ? 'Обработка вопроса' : 'Отправить вопрос'}
              title="Отправить вопрос (Enter)"
            >
              {pending ? (
                <LoaderCircle size={20} className={'animate-spin'} />
              ) : (
                <ArrowUp size={20} />
              )}
            </button>
          </div>
        </div>
      </form>
      <p id="input-hint" className={ui['sr-only']}>
        Enter — отправить. Shift + Enter — новая строка.
      </p>
    </div>
  );
}
