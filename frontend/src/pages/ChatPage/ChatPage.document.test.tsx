// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { Link, MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { chatApi, type AnswerMode, type ChatHistory, type ChatSource } from '../../features/chat';
import { templatesApi } from '../../features/templates';
import * as api from '../../shared/api/client';
import type { DocumentView } from './ChatDocumentPanel';
import { ChatPage } from './ChatPage';

const source: ChatSource = {
  chunk_id: 'chunk', document_id: 'document', document_version_id: 'version', document_title: 'Инструкция по делопроизводству',
  version_number: 1, chunk_index: 0, content: 'Матрица определяет согласующих.', page_number: 4,
  section_number: null, section_title: null, score: 1, position: 1,
};
function history(mode: AnswerMode = 'generated', sources: ChatSource[] = []): ChatHistory {
  return {
    session: { id: 'chat', title: 'Матрица визирования', created_at: '2026-10-25T10:00:00Z', updated_at: '2026-10-25T10:00:00Z', archived_at: null },
    messages: [{ id: 'answer', session_id: 'chat', role: 'assistant', content: 'Ответ на нормативный вопрос о документе.', mode, sources, created_at: '2026-10-25T10:00:00Z' }],
    limit: 100, offset: 0,
  };
}
let host: HTMLDivElement;
let root: Root;
let client: QueryClient;
let serverHistory: ChatHistory;
let serverView: DocumentView;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  host = document.createElement('div'); document.body.append(host); root = createRoot(host);
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  serverHistory = history(); serverView = { enabled: true, workflow: null };
  vi.spyOn(chatApi, 'history').mockImplementation(async id => id === 'chat' ? serverHistory : { ...history(), session: { ...history().session, id }, messages: [] });
  vi.spyOn(chatApi, 'list').mockResolvedValue({ sessions: [serverHistory.session], limit: 20, offset: 0 });
  vi.spyOn(api, 'apiRequest').mockImplementation(async path => path === '/chat/sessions/chat/document' ? serverView as never : { enabled: true } as never);
});
afterEach(async () => {
  await act(async () => root.unmount()); client.clear(); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals();
});
async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function mount() {
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/chat?session=chat']}>
    <Link to="/chat?session=other">Другой чат</Link><ChatPage />
  </MemoryRouter></QueryClientProvider>));
  await flush(); await flush();
}
function expectCleanChat() {
  expect(host.querySelector('section[aria-label="Подготовка документа"]')).toBeNull();
  expect(host.textContent).not.toMatch(/Создать документ в чате|Начать подготовку/);
  expect(host.querySelector('textarea')?.disabled).toBe(false);
}

describe('server document workflow visibility in ChatPage', () => {
  it.each(['generated', 'search', 'no_sources', 'clarification'] as const)('keeps %s answers clean with enabled=true and no workflow', async mode => {
    serverHistory = history(mode, ['generated', 'search'].includes(mode) ? [source] : []);
    await mount();
    expect(host.textContent).toContain('Ответ на нормативный вопрос');
    if (serverHistory.messages[0]!.sources.length) {
      expect(host.textContent).toContain('Источники');
      expect(host.textContent).toContain(source.document_title);
    }
    expectCleanChat();
    expect(vi.mocked(api.apiRequest).mock.calls.every(([, options]) => !options?.method || options.method === 'GET')).toBe(true);
  });
  it('keeps ordinary RAG available with the feature disabled', async () => {
    serverView = { enabled: false }; serverHistory = history('generated', [source]);
    await mount(); expectCleanChat(); expect(host.textContent).toContain(source.document_title);
  });
  it('shows the workflow created by the server after a create_document chat turn', async () => {
    await mount(); expectCleanChat();
    vi.spyOn(chatApi, 'send').mockImplementation(async (_id, text) => {
      serverView = { enabled: true, workflow: { stage: 'selecting_template', version: 1, reviewed_revision: 0 },
        options: [{ id: 'working-group', name: 'Распоряжение о рабочей группе', description: 'Выберите шаблон' }] };
      serverHistory = { ...history('document'), messages: [
        { ...history().messages[0]!, id: 'request', role: 'user', content: text, mode: null },
        { ...history('document').messages[0]!, content: 'Выберите подходящий шаблон.' },
      ] };
      return { user_message_id: 'request', assistant_message_id: 'answer', mode: 'document', message: 'Выберите подходящий шаблон.', sources: [], user_created_at: '', assistant_created_at: '' };
    });
    const input = host.querySelector('textarea')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'Мне нужно подготовить распоряжение о рабочей группе');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => input.closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
    await flush(); await flush();
    expect(chatApi.send).toHaveBeenCalledTimes(1);
    expect(host.querySelector('section[aria-label="Подготовка документа"]')).not.toBeNull();
    expect(host.textContent).toContain('Распоряжение о рабочей группе');
    expect(vi.mocked(api.apiRequest).mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
  });
  it('removes the preceding workflow on a session change', async () => {
    serverView = { enabled: true, workflow: { stage: 'selecting_template', version: 1, reviewed_revision: 0 },
      options: [{ id: 'private', name: 'Предыдущий шаблон', description: '' }] };
    await mount(); expect(host.textContent).toContain('Предыдущий шаблон');
    await act(async () => [...host.querySelectorAll('a')].find(a => a.textContent === 'Другой чат')!.click());
    expect(host.textContent).not.toContain('Предыдущий шаблон');
    await flush(); await flush(); expectCleanChat();
  });
  it('keeps RAG and the composer available after a workflow load error', async () => {
    vi.mocked(api.apiRequest).mockRejectedValue(new api.ApiError(503));
    await mount(); expectCleanChat();
    expect(host.textContent).toContain('Не удалось загрузить состояние подготовки');
    expect(host.textContent).toContain('Ответ на нормативный вопрос');
  });
  it('restores one authorized DOCX beside its history message after reload', async () => {
    serverHistory.messages[0]!.document_generated_id = 'artifact';
    serverView = { enabled: true, workflow: { stage: 'completed', version: 2, reviewed_revision: 1 },
      generated: { id: 'artifact', filename: 'result.docx', draft_revision: 1 } };
    vi.spyOn(templatesApi, 'generated').mockResolvedValue({ id: 'artifact', draft_id: 'draft', template_id: 'template', document_version_id: 'version', draft_revision: 1,
      filename: 'result.docx', mime_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', size_bytes: 100, sha256: 'hash', created_at: '' });
    await mount();
    await act(async () => root.render(null));
    await mount();
    expect([...host.querySelectorAll('button')].filter(b => b.textContent === 'Скачать DOCX')).toHaveLength(1);
    expect(host.querySelector('[role="log"]')?.textContent).toContain('Скачать DOCX');
    expectCleanChat();
  });
});
