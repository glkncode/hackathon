import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider, QueryObserver } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { chatHistoryOptions } from '../hooks/useChatHistory';
import { ChatPage } from './ChatPage';
import type { AnswerMode, ChatHistory, ChatSource } from '../types/api';

const fetchMock = vi.fn<typeof fetch>();
const clients: QueryClient[] = [];
const privateText = 'Закрытые сведения из первоначального ответа.';
const replacement = 'Содержимое ответа недоступно из-за изменения прав доступа к документам.';
const privateSource: ChatSource = {
  chunk_id: 'private-chunk',
  document_id: 'private-document',
  document_version_id: 'private-version',
  document_title: 'Закрытый регламент',
  version_number: 2,
  chunk_index: 0,
  content: 'Закрытый фрагмент исходного документа.',
  page_number: 13,
  section_number: '4.6',
  section_title: null,
  score: 0.4,
  position: 1,
};

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('document', { cookie: '' });
});

afterEach(() => {
  clients.forEach((client) => client.clear());
  clients.length = 0;
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function createClient() {
  const client = new QueryClient({
    // History must refresh even if a caller otherwise permits fresh cached data.
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  clients.push(client);
  return client;
}

function history(content: string, mode: AnswerMode, sources: ChatSource[] = []): ChatHistory {
  return {
    session: {
      id: 'session',
      title: 'Проверка режимов',
      archived_at: null,
      created_at: '2026-10-24T00:00:00Z',
      updated_at: '2026-10-24T00:00:00Z',
    },
    messages: [
      {
        id: 'answer',
        session_id: 'session',
        role: 'assistant',
        content,
        mode,
        created_at: '2026-10-24T00:00:00Z',
        sources,
      },
    ],
    limit: 100,
    offset: 0,
  };
}

function respond(value: ChatHistory) {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(value)));
}

function renderPage(client: QueryClient) {
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/chat?session=session']}>
        <ChatPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('current server history in ChatPage', () => {
  it.each(['generated', 'clarification'] as const)(
    'replaces cached %s content with the safe API text after a history refresh',
    async (mode) => {
      const client = createClient();
      const options = chatHistoryOptions('session');
      respond(history(privateText, mode, [privateSource]));
      const observer = new QueryObserver(client, options);
      const unsubscribe = observer.subscribe(() => {});
      try {
        await vi.waitFor(() => expect(observer.getCurrentResult().isSuccess).toBe(true));
        const initialHtml = renderPage(client);
        expect(initialHtml).toContain(privateText);
        expect(initialHtml).toContain(privateSource.document_title);
        expect(initialHtml).toContain(privateSource.content);

        respond(history(replacement, mode));
        await client.invalidateQueries({ queryKey: options.queryKey });
        const html = renderPage(client);
        expect(html).toContain(replacement);
        expect(html).not.toContain(privateText);
        expect(html).not.toContain(privateSource.document_title);
        expect(html).not.toContain(privateSource.content);
        expect(html).not.toContain('class="source-card"');
        expect(client.getQueryData<ChatHistory>(options.queryKey)?.messages[0]?.content).toBe(
          replacement,
        );
        expect(fetchMock).toHaveBeenCalledTimes(2);
      } finally {
        unsubscribe();
      }
    },
  );

  it('shows the updated server mode and leaves the composer available after clarification', async () => {
    const client = createClient();
    const options = chatHistoryOptions('session');
    respond(history('Первоначальный ответ.', 'generated'));
    const observer = new QueryObserver(client, options);
    const unsubscribe = observer.subscribe(() => {});
    try {
      await vi.waitFor(() => expect(observer.getCurrentResult().isSuccess).toBe(true));
      const question = 'Какой именно регламент вы имеете в виду?';
      respond(history(question, 'clarification'));
      await client.invalidateQueries({ queryKey: options.queryKey });

      const html = renderPage(client);
      expect(html).toContain('Уточняющий вопрос');
      expect(html).toContain(question);
      expect(html).not.toContain('Первоначальный ответ.');
      expect(html).not.toContain('Ответ AI');
      expect(html).toMatch(/<textarea\b[^>]*id="chat-message"/);
      expect(html).not.toMatch(/<textarea\b[^>]*\bdisabled/);
      expect(html).not.toContain('изменились права доступа');
    } finally {
      unsubscribe();
    }
  });

  it('requests current API data on reopening a conversation even when history is already cached', async () => {
    const client = createClient();
    const options = chatHistoryOptions('session');
    client.setQueryData(options.queryKey, history(privateText, 'generated', [privateSource]));
    respond(history(replacement, 'generated'));
    const observer = new QueryObserver(client, options);
    const unsubscribe = observer.subscribe(() => {});
    try {
      await vi.waitFor(() =>
        expect(observer.getCurrentResult().data?.messages[0]?.content).toBe(replacement),
      );
      const html = renderPage(client);
      expect(html).toContain(replacement);
      expect(html).not.toContain(privateText);
      expect(html).not.toContain(privateSource.content);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/chat/sessions/session?limit=100&offset=0');
      expect(fetchMock.mock.calls[0]?.[1]?.cache).toBe('no-store');
    } finally {
      unsubscribe();
    }
  });
});
