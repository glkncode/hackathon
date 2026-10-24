import {
  FileText,
  MessageCircleQuestion,
  Search,
  SearchX,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import type { AnswerMode, LocalMessage } from '../../types/api';
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
      <article className="user-message" aria-label="Ваш вопрос">
        <div>{message.content}</div>
      </article>
    );
  const { label, icon: ModeIcon } = message.mode
    ? answerModes[message.mode]
    : { label: 'Сообщение ассистента', icon: FileText };
  return (
    <article className="assistant-message" aria-label={label}>
      <div className="assistant-label">
        <span className="assistant-avatar">
          <FileText size={17} />
        </span>
        <strong>AI Document Assistant</strong>
        <span className="answer-mode">
          <ModeIcon size={13} />
          {label}
        </span>
      </div>
      <div className="answer-body">
        <p className="answer-text">{message.content}</p>
        {message.sources.length > 0 && (
          <>
            <div className="sources-heading">
              ИСТОЧНИКИ <span>{message.sources.length}</span>
            </div>
            <div className="sources-list">
              {message.sources.map((source, index) => (
                <SourceCard key={source.chunk_id} source={source} index={index} />
              ))}
            </div>
          </>
        )}
        {message.mode === 'search' && (
          <p className="search-disclaimer">
            Найденные фрагменты документов. Поиск не формирует генеративный ответ.
          </p>
        )}
      </div>
    </article>
  );
}
