import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { chatApi } from '../api/chat';
import { searchApi } from '../api/search';
import type { LocalMessage } from '../types/chat';
import { useChatHistory } from './useChatHistory';
import { sessionsKey, useChatSessions } from './useChatSessions';

interface SendAttempt {
  text: string;
  id: string;
  sessionId: string | null;
}

export function useChatController(sessionId: string | null) {
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
    if (container) container.scrollTop = messageCount > 0 || pending ? container.scrollHeight : 0;
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

  return {
    sessions,
    available,
    history,
    archive,
    ready,
    pending,
    messages,
    send,
    submit,
    draft,
    setDraft,
    attempt,
    scrollContainer,
  };
}
