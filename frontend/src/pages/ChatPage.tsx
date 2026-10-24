import { useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  ArrowUpRight,
  BookOpen,
  FileCheck2,
  FileText,
  Info,
  MessageSquare,
  Search,
  Trash2,
} from 'lucide-react';
import { chatApi } from '../api/chat';
import { ApiError } from '../api/client';
import { searchApi } from '../api/search';
import { useChatSessions, sessionsKey } from '../hooks/useChatSessions';
import { useChatHistory } from '../hooks/useChatHistory';
import { ChatInput } from '../components/chat/ChatInput';
import { ChatMessage } from '../components/chat/ChatMessage';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import type { LocalMessage } from '../types/api';

const examples = [
  {
    icon: FileCheck2,
    title: 'Согласование документов',
    question: 'Как согласовать служебную записку?',
  },
  {
    icon: FileText,
    title: 'Правила оформления',
    question: 'Какие требования предъявляются к оформлению документа?',
  },
  {
    icon: BookOpen,
    title: 'Регистрация и учёт',
    question: 'Где найти порядок регистрации документов?',
  },
];

interface SendAttempt {
  text: string;
  id: string;
  sessionId: string | null;
}

export function ChatPage() {
  const [params] = useSearchParams();
  const sessionId = params.get('session');
  const navigate = useNavigate();
  const client = useQueryClient();
  const sessions = useChatSessions();
  const available = sessions.data?.pages[0]?.available;
  const [draft, setDraft] = useState('');
  const [localMessages, setLocalMessages] = useState<LocalMessage[]>([]);
  const attempt = useRef<SendAttempt | null>(null);
  const requestController = useRef<AbortController | null>(null);
  const scrollContainer = useRef<HTMLDivElement>(null);
  const historyKey = ['chat', 'history', sessionId] as const;
  const history = useChatHistory(sessionId);

  useEffect(() => () => requestController.current?.abort(), []);

  const send = useMutation({
    mutationFn: async (current: SendAttempt) => {
      const controller = new AbortController();
      requestController.current = controller;
      const { signal } = controller;
      if (!sessionId && available === false && !current.sessionId) {
        const result = await searchApi.search(current.text, signal);
        return { kind: 'search' as const, result, current };
      }
      // Retry keeps both the created session and the idempotency key, including
      // when the response was lost after the backend committed a turn.
      if (!current.sessionId) {
        const created = await chatApi.create(
          Array.from(current.text).slice(0, 80).join(''),
          signal,
        );
        current.sessionId = created.id;
        void client.invalidateQueries({ queryKey: sessionsKey });
      }
      const result = await chatApi.send(current.sessionId, current.text, current.id, signal);
      return { kind: 'chat' as const, result, current };
    },
    onSuccess: async (response) => {
      setDraft('');
      attempt.current = null;
      if (response.kind === 'search') {
        const { result, current } = response;
        const mode = result.results.length ? 'search' : 'no_sources';
        setLocalMessages((messages) => [
          ...messages,
          { id: current.id, role: 'user', content: current.text, mode: null, sources: [] },
          {
            id: crypto.randomUUID(),
            role: 'assistant',
            mode,
            sources: result.results,
            content: result.results.length
              ? 'Найдены подходящие фрагменты документов.'
              : 'В доступных документах не найдена информация для ответа. Попробуйте уточнить вопрос или использовать другие слова.',
          },
        ]);
      } else {
        await client.invalidateQueries({ queryKey: sessionsKey });
        if (sessionId) await client.invalidateQueries({ queryKey: historyKey });
        else
          navigate(`/chat?session=${encodeURIComponent(response.current.sessionId!)}`, {
            replace: true,
          });
      }
    },
  });

  const archive = useMutation({
    mutationFn: () => chatApi.archive(sessionId!),
    onSuccess: async () => {
      client.removeQueries({ queryKey: historyKey });
      await client.invalidateQueries({ queryKey: sessionsKey });
      navigate('/chat', { replace: true });
    },
  });

  const messages: LocalMessage[] = sessionId ? (history.data?.messages ?? []) : localMessages;
  const ready = sessionId
    ? history.isSuccess && !history.data.session.archived_at
    : available !== undefined && !sessions.isError;
  const pending = send.isPending;
  const messageCount = messages.length;
  useEffect(() => {
    // Scroll only the message list: scrollIntoView also moves overflow-hidden
    // ancestors and can push the application header and composer off screen.
    const container = scrollContainer.current;
    if (container) container.scrollTop = container.scrollHeight;
  }, [messageCount, pending]);

  function submit() {
    if (
      !ready ||
      pending ||
      !draft.trim() ||
      Array.from(draft).length > 500 ||
      draft.includes('\0')
    )
      return;
    const text = draft.trim();
    if (!attempt.current || attempt.current.text !== text) {
      attempt.current = {
        text,
        id: crypto.randomUUID(),
        sessionId: attempt.current?.sessionId ?? sessionId,
      };
    }
    send.mutate(attempt.current);
  }

  return (
    <section className="chat-page">
      {sessionId && (
        <div className="conversation-top">
          <h1>{history.data?.session.title ?? 'Сохранённый диалог'}</h1>
          {history.isSuccess && (
            <button
              className="text-button archive-button"
              disabled={archive.isPending || pending}
              onClick={() => archive.mutate()}
            >
              <Trash2 size={14} />
              {archive.isPending ? 'Архивируем…' : 'В архив'}
            </button>
          )}
        </div>
      )}
      <div className="chat-scroll" ref={scrollContainer}>
        <div className="chat-content">
          {sessionId && history.isPending && <Loading label="Загружаем диалог…" />}
          {sessionId && history.isError && (
            <ErrorNotice error={history.error} onRetry={() => void history.refetch()} />
          )}
          {archive.isError && <ErrorNotice error={archive.error} />}
          {!sessionId && sessions.isError && (
            <ErrorNotice error={sessions.error} onRetry={() => void sessions.refetch()} />
          )}
          {available === false && !sessionId && (
            <div className="temporary-notice">
              <Info size={16} />
              <span>
                Временный диалог: сообщения хранятся только на этой странице. Серверная история пока
                недоступна.
              </span>
            </div>
          )}
          {sessionId && history.data?.session.archived_at && (
            <div className="temporary-notice">
              <Info size={16} />
              Этот диалог в архиве. Создайте новый, чтобы задать вопрос.
            </div>
          )}
          {messages.length === 0 && !pending && (!sessionId || history.isSuccess) && (
            <div className="chat-welcome">
              <div className="welcome-mark">
                <FileText size={31} />
                <span>
                  <Search size={15} />
                </span>
              </div>
              <span className="eyebrow">ВАШ ПОМОЩНИК ПО ДОКУМЕНТАМ</span>
              <h1>
                Здравствуйте! Я помогу разобраться
                <br className="desktop-break" /> во внутренних инструкциях
                <br className="desktop-break" /> и регламентах
              </h1>
              <p>Задайте вопрос, и я найду подходящие положения документов.</p>
              <div className="example-grid">
                {examples.map(({ icon: Icon, title, question }) => (
                  <button
                    key={title}
                    className="example-card"
                    disabled={!ready || pending}
                    onClick={() => setDraft(question)}
                  >
                    <span className="example-icon">
                      <Icon size={19} />
                    </span>
                    <strong>{title}</strong>
                    <span className="example-question">{question}</span>
                    <ArrowUpRight className="example-arrow" size={16} />
                  </button>
                ))}
              </div>
              <div className="welcome-caption">
                <Search size={14} />
                Примеры вопросов. Результаты появятся после поиска.
              </div>
            </div>
          )}
          {messages.length > 0 && (
            <div className="messages" role="log" aria-label="Сообщения диалога" aria-live="polite">
              {messages.map((message) => (
                <ChatMessage key={message.id} message={message} />
              ))}
            </div>
          )}
          {pending && (
            <div className="messages pending-turn">
              <ChatMessage
                message={{
                  id: 'pending',
                  role: 'user',
                  content: attempt.current?.text ?? '',
                  mode: null,
                  sources: [],
                }}
              />
              <Loading label="Ищем в доступных документах…" />
            </div>
          )}
          {send.isError && <ErrorNotice error={send.error} onRetry={submit} />}
          {send.isError && send.error instanceof ApiError && send.error.status === 409 && (
            <p className="search-disclaimer">
              Если диалог архивирован, создайте новый. При изменении источников можно повторить
              вопрос.
            </p>
          )}
        </div>
      </div>
      <div className="chat-bottom">
        <ChatInput
          value={draft}
          onChange={(value) => {
            setDraft(value);
            if (send.isError) send.reset();
          }}
          onSend={submit}
          pending={pending}
          disabled={!ready || archive.isPending}
        />
        <div className="chat-footer">
          <MessageSquare size={13} />
          <span>Результаты из доступных вам документов</span>
        </div>
      </div>
    </section>
  );
}
