import {
  Check,
  Copy,
  FileText,
  MessageCircleQuestion,
  Search,
  SearchX,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import ui from '../../../shared/ui/ui.module.css';
import type { AnswerMode, LocalMessage } from '../types/chat';
import styles from './ChatMessage.module.css';
import { SourceCard } from './SourceCard';

const answerModes: Record<AnswerMode, { label: string; icon: LucideIcon }> = {
  document: { label: 'Подготовка документа', icon: FileText },
  generated: { label: 'Ответ помощника', icon: Sparkles },
  clarification: { label: 'Уточняющий вопрос', icon: MessageCircleQuestion },
  search: { label: 'Поиск по документам', icon: Search },
  no_sources: { label: 'Информация не найдена', icon: SearchX },
};

export function ChatMessage({ message }: { message: LocalMessage }) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  if (message.role === 'user')
    return (
      <article className={styles['user-message']} aria-label="Ваш вопрос">
        <div>{message.content}</div>
      </article>
    );
  const { label, icon: ModeIcon } = message.mode
    ? answerModes[message.mode]
    : { label: 'Сообщение ассистента', icon: FileText };
  return (
    <article className={styles['assistant-message']} aria-label={label}>
      <div className={styles['assistant-label']}>
        <span className={styles['assistant-avatar']}>
          <FileText size={17} />
        </span>
        <strong>Помощник по документам</strong>
        <span className={styles['answer-mode']}>
          <ModeIcon size={13} />
          {label}
        </span>
      </div>
      <div className={styles['answer-body']}>
        <div className={styles['answer-text']}>
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              // Document content remains text; never load model-provided images or HTML.
              img: ({ alt }) => <span>{alt}</span>,
              a: ({ children, href }) =>
                href ? (
                  <a href={href} target="_blank" rel="noopener noreferrer">
                    {children}
                  </a>
                ) : (
                  <span>{children}</span>
                ),
              table: ({ children }) => (
                <div
                  className={styles['table-scroll']}
                  tabIndex={0}
                  role="region"
                  aria-label="Таблица в ответе"
                >
                  <table>{children}</table>
                </div>
              ),
            }}
          >
            {message.content}
          </ReactMarkdown>
        </div>
        {message.sources.length > 0 && (
          <>
            <div className={styles['sources-heading']}>
              Источники <span>{message.sources.length}</span>
            </div>
            <div className={styles['sources-list']}>
              {message.sources.map((source, index) => (
                <SourceCard key={source.chunk_id} source={source} index={index} />
              ))}
            </div>
          </>
        )}
        {message.mode === 'search' && (
          <p className={styles['search-disclaimer']}>
            Найденные фрагменты документов. Поиск не формирует генеративный ответ.
          </p>
        )}
        <div className={styles['answer-actions']}>
          <button
            className={ui['text-button']}
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(message.content);
                setCopyState('copied');
              } catch {
                setCopyState('failed');
              }
            }}
          >
            {copyState === 'copied' ? <Check size={17} /> : <Copy size={17} />}
            {copyState === 'copied' ? 'Скопировано' : 'Скопировать ответ'}
          </button>
          <span role="status" className={ui['muted']}>
            {copyState === 'failed'
              ? 'Не удалось скопировать. Выделите и скопируйте текст вручную.'
              : ''}
          </span>
        </div>
      </div>
    </article>
  );
}
