import { useEffect, useRef } from 'react';
import { ArrowUp, LoaderCircle } from 'lucide-react';

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
    <div className="composer-wrap">
      <form
        className={`composer ${invalid ? 'invalid' : ''}`}
        onSubmit={(event) => {
          event.preventDefault();
          if (canSend) onSend();
        }}
      >
        <label className="sr-only" htmlFor="chat-message">
          Вопрос по документам
        </label>
        <textarea
          ref={ref}
          id="chat-message"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder="Задайте вопрос по вашим документам…"
          rows={1}
          disabled={pending || disabled}
          aria-describedby="input-hint"
          aria-invalid={invalid || undefined}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault();
              if (canSend) onSend();
            }
          }}
        />
        <div className="composer-bottom">
          <span className={invalid ? 'input-invalid' : ''}>
            {pending
              ? 'Ищем подходящие положения…'
              : invalid
                ? 'До 500 символов, без нулевого символа'
                : 'Вопрос по внутренним документам'}
          </span>
          <div className="composer-controls">
            <span className="character-count">{length}/500</span>
            <button
              className="send-button"
              type="submit"
              disabled={!canSend}
              aria-label={pending ? 'Обработка вопроса' : 'Отправить вопрос'}
            >
              {pending ? (
                <LoaderCircle size={20} className="animate-spin" />
              ) : (
                <ArrowUp size={20} />
              )}
            </button>
          </div>
        </div>
      </form>
      <p id="input-hint" className="composer-hint">
        Enter — отправить<span>·</span>Shift + Enter — новая строка
      </p>
    </div>
  );
}
