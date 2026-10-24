// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import type { Root } from 'react-dom/client';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatHistory } from '../../features/chat';
import { chatApi, chatHistoryOptions } from '../../features/chat';
import { ChatPage } from './ChatPage';
import pageStyles from './ChatPage.module.css';

const session: ChatHistory['session'] = {
  id: 'long-chat',
  title: 'Длинный диалог',
  archived_at: null,
  created_at: '2026-10-24T00:00:00Z',
  updated_at: '2026-10-24T00:00:00Z',
};

function history(count: number): ChatHistory {
  return {
    session,
    messages: Array.from({ length: count }, (_, index) => ({
      id: `answer-${index}`,
      session_id: session.id,
      role: 'assistant',
      mode: 'generated',
      content: `Ответ ${index + 1}. ${'Подробный текст. '.repeat(50)}`,
      sources: [],
      created_at: session.created_at,
    })),
    limit: 100,
    offset: 0,
  };
}

let host: HTMLDivElement;
let root: Root;
let client: QueryClient;
let contentHeight: number;
const scrollIntoView = vi.fn();
const originalScrollIntoView = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollIntoView');

beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  contentHeight = 6000;
  // jsdom has no layout engine. Browser geometry is checked separately; these
  // tests exercise which element React scrolls when long history changes.
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (
    this: HTMLElement,
  ) {
    return this.classList.contains(pageStyles['chat-scroll'] ?? '') ? contentHeight : 32;
  });
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    value: scrollIntoView,
  });
  scrollIntoView.mockClear();
  vi.spyOn(chatApi, 'history').mockResolvedValue(history(20));
  vi.spyOn(chatApi, 'list').mockResolvedValue({ sessions: [session], limit: 20, offset: 0 });
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(chatHistoryOptions(session.id).queryKey, history(20));
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  client.clear();
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  if (originalScrollIntoView) {
    Object.defineProperty(Element.prototype, 'scrollIntoView', originalScrollIntoView);
  } else {
    Reflect.deleteProperty(Element.prototype, 'scrollIntoView');
  }
});

async function mount() {
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[`/chat?session=${session.id}`]}>
          <ChatPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
}

function messageViewport() {
  const element = host.querySelector<HTMLDivElement>('.' + pageStyles['chat-scroll']);
  if (!element) throw new Error('Message viewport is missing');
  return element;
}

describe('long chat scrolling', () => {
  it('scrolls the message viewport on opening history without scrolling ancestors', async () => {
    await mount();

    expect(host.querySelectorAll('article[aria-label="Ответ AI"]')).toHaveLength(20);
    expect(messageViewport().scrollTop).toBe(contentHeight);
    expect(host.scrollTop).toBe(0);
    expect(document.documentElement.scrollTop).toBe(0);
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('scrolls to new server messages while keeping the composer mounted', async () => {
    await mount();
    const composer = host.querySelector('textarea');
    contentHeight = 9000;
    await act(async () => {
      client.setQueryData(chatHistoryOptions(session.id).queryKey, history(21));
    });
    await vi.waitFor(async () => {
      await act(async () => {});
      expect(messageViewport().textContent).toContain('Ответ 21.');
      expect(messageViewport().scrollTop).toBe(contentHeight);
    });

    expect(host.querySelector('textarea')).toBe(composer);
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('focuses the composer without moving outer scroll containers', async () => {
    const focus = vi.spyOn(HTMLElement.prototype, 'focus');
    await mount();

    expect(document.activeElement).toBe(host.querySelector('textarea'));
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });
});
