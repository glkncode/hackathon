import {
  FileText,
  MessageCircleQuestion,
  Search,
  SearchX,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import type { AnswerMode, LocalMessage } from '../types/chat';
import styles from './ChatMessage.module.css';
import { SourceCard } from './SourceCard';

const answerModes: Record<AnswerMode, { label: string; icon: LucideIcon }> = {
  generated: { label: 'Ответ AI', icon: Sparkles },
  clarification: { label: 'Уточняющий вопрос', icon: MessageCircleQuestion },
  search: { label: 'Поиск по документам', icon: Search },
  no_sources: { label: 'Информация не найдена', icon: SearchX },
};

export function ChatMessage({ message }: { message: LocalMessage }) {
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
    <article aria-label={label}>
      <div className={styles['assistant-label']}>
        <span className={styles['assistant-avatar']}>
          <FileText size={17} />
        </span>
        <strong>AI Document Assistant</strong>
        <span className={styles['answer-mode']}>
          <ModeIcon size={13} />
          {label}
        </span>
      </div>
      <div className={styles['answer-body']}>
        <p className={styles['answer-text']}>{message.content}</p>
        {message.sources.length > 0 && (
          <>
            <div className={styles['sources-heading']}>
              ИСТОЧНИКИ <span>{message.sources.length}</span>
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
      </div>
    </article>
  );
}
