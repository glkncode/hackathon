// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { templatesApi, type TemplateDraft } from '../../features/templates';
import * as api from '../../shared/api/client';
import { ChatDocumentPanel, type DocumentView } from './ChatDocumentPanel';

const draft: TemplateDraft = {
  id: 'same-wizard-draft', template_id: 'registered', document_version_id: 'pinned', owner_id: 'owner',
  values: { purpose: 'Тестовая цель' }, status: 'ready_to_generate', revision: 7,
  fields: [{ key: 'purpose', label: 'Цель', question: 'Для чего?', type: 'text', required: true, description: '', max_length: 100 }],
  schema_version: 2, required_field_count: 1, completed_field_count: 1,
  validation: { ready: true, next_question: null, missing_fields: [], invalid_fields: [], visible_fields: ['purpose'], required_fields: ['purpose'],
    checks: [{ rule_id: 'manual', category: 'warning', source: { name: 'Профиль' }, description: 'Нужна проверка полномочий подписанта', result: 'warning', method: 'profile' }] },
  created_at: '2026-10-25T10:00:00Z', updated_at: '2026-10-25T10:00:00Z',
};
let host: HTMLDivElement;
let root: Root;
let client: QueryClient;
let view: DocumentView;
let requests: { path: string; body?: unknown }[];

beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  host = document.createElement('div'); document.body.append(host); root = createRoot(host);
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  view = { enabled: true, workflow: { stage: 'reviewing', version: 4, reviewed_revision: 7 }, draft: structuredClone(draft), template_name: 'Учебный шаблон' };
  requests = [];
  vi.spyOn(api, 'apiRequest').mockImplementation(async (path, options) => {
    requests.push({ path, body: options?.body });
    return path.endsWith('/actions') ? {} : view as never;
  });
});
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function render(sessionId = 'chat', updatedAt = 1, generatedInHistory: string[] = []) {
  await act(async () => { root.render(<QueryClientProvider client={client}><MemoryRouter><ChatDocumentPanel key={sessionId} sessionId={sessionId} updatedAt={updatedAt} pending={false} generatedInHistory={generatedInHistory} /></MemoryRouter></QueryClientProvider>); });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); });
}
describe('document preparation inside chat', () => {
  it.each([null, undefined])('shows nothing when enabled but workflow is %s', async workflow => {
    view = { enabled: true, workflow };
    await render();
    expect(host.textContent).toBe('');
    expect(requests.every(r => r.body === undefined)).toBe(true);
  });
  it('keeps the flag off without showing document controls', async () => {
    view = { enabled: false }; await render(); expect(host.textContent).toBe('');
  });
  it('shows saved summary, authoritative warnings and the same Wizard link', async () => {
    await render();
    expect(host.textContent).toContain('Тестовая цель');
    expect(host.textContent).toContain('Нужна проверка полномочий подписанта');
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/templates/drafts/same-wizard-draft');
    const button = [...host.querySelectorAll('button')].find(b => b.textContent === 'Создать DOCX')!;
    await act(async () => button.click());
    const mutation = requests.find(r => r.path.endsWith('/actions'))!;
    expect(mutation.body).toMatchObject({ action: 'generate', expected_revision: 7, workflow_version: 4 });
    expect((mutation.body as { client_message_id: string }).client_message_id).toMatch(/^[a-f0-9-]{36}$/);
  });
  it('does not offer generation when required fields are missing', async () => {
    view.draft!.validation!.ready = false;
    view.draft!.validation!.next_question = view.draft!.fields[0]!;
    view.workflow!.stage = 'collecting_fields';
    await render();
    expect([...host.querySelectorAll('button')].some(b => b.textContent === 'Создать DOCX')).toBe(false);
    expect(host.textContent).toContain('Ввести ответ в форме');
    expect(host.querySelector('progress')?.value).toBe(1);
    expect(host.querySelector('input, textarea')).not.toBeNull();
  });
  it('removes values and controls when access is revoked', async () => {
    await render();
    expect(host.textContent).toContain('Тестовая цель');
    view = { enabled: true, unavailable: true }; await render('chat', 2);
    expect(host.textContent).toContain('Доступ к шаблону изменён');
    expect(host.textContent).not.toContain('Тестовая цель');
    expect(host.querySelector('button')).toBeNull();
  });
  it('offers compact resume without fields or validation while paused', async () => {
    view.workflow!.stage = 'paused';
    await render();
    expect(host.textContent).toContain('Подготовка документа приостановлена');
    expect(host.querySelector('section, progress, input, textarea, details')).toBeNull();
    expect(host.textContent).not.toContain('Тестовая цель');
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'Продолжить')!.click());
    expect(requests.find(r => r.body)?.body).toMatchObject({ action: 'resume', workflow_version: 4 });
  });
  it('keeps cancelled drafts accessible without an active wizard', async () => {
    view.workflow!.stage = 'cancelled';
    await render();
    expect(host.querySelector('section, button, progress')).toBeNull();
    expect(host.querySelector('a')?.getAttribute('href')).toContain(draft.id);
  });
  it.each(['completed', 'cancelled'] as const)('preserves an authorized DOCX for %s without generation controls', async stage => {
    view.workflow!.stage = stage;
    view.generated = { id: 'real-artifact', filename: 'result.docx', draft_revision: 7 };
    const metadata = vi.spyOn(templatesApi, 'generated').mockResolvedValue({
      id: 'real-artifact', draft_id: draft.id, template_id: 'registered', document_version_id: 'pinned',
      draft_revision: 7, filename: 'result.docx', mime_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      size_bytes: 100, sha256: 'hash', created_at: draft.created_at,
    });
    const download = vi.spyOn(templatesApi, 'download').mockResolvedValue();
    await render();
    await vi.waitFor(() => expect(host.textContent).toContain('Скачать DOCX'));
    expect(host.querySelector('section, progress, details')).toBeNull();
    await act(async () => host.querySelector('button')!.click());
    expect(metadata).toHaveBeenCalledWith('real-artifact', expect.any(AbortSignal));
    expect(download).toHaveBeenCalledWith('real-artifact', 'result.docx');
    await render('chat', 1, ['real-artifact']);
    expect(host.textContent).toBe('');
  });
  it('hides a generated file after its metadata ACL check fails', async () => {
    view.workflow!.stage = 'completed';
    view.generated = { id: 'revoked-artifact', filename: 'secret.docx', draft_revision: 7 };
    vi.spyOn(templatesApi, 'generated').mockRejectedValue(new api.ApiError(403));
    await render();
    expect(host.textContent).toBe('');
  });
  it('restores an active workflow on remount using GET only', async () => {
    await render();
    await act(async () => root.render(null));
    await render();
    expect(host.textContent).toContain('Тестовая цель');
    expect(requests.every(r => r.body === undefined)).toBe(true);
  });
  it('does not reuse previous session data while another workflow request is pending', async () => {
    await render();
    vi.mocked(api.apiRequest).mockImplementation(() => new Promise(() => {}));
    await render('another-chat');
    expect(host.textContent).toBe('');
    expect(client.getQueryData(['chat', 'document', 'another-chat'])).toBeUndefined();
  });
  it('hides cached fields on reopening until the server rechecks ACL', async () => {
    client.setQueryData(['chat', 'document', 'chat'], view);
    vi.mocked(api.apiRequest).mockImplementation(() => new Promise(() => {}));
    await render();
    expect(host.textContent).toBe('');
  });
  it('distinguishes load errors from a workflow and allows retry', async () => {
    vi.mocked(api.apiRequest).mockRejectedValue(new api.ApiError(503));
    await render();
    expect(host.textContent).toContain('Не удалось загрузить состояние');
    expect(host.querySelector('section, progress')).toBeNull();
    expect(host.textContent).not.toContain('Тестовая цель');
  });
  it('shows server template choices only for an existing selection workflow', async () => {
    view = { enabled: true, workflow: { stage: 'selecting_template', version: 1, reviewed_revision: 0 },
      options: [{ id: 'registered', name: 'Распоряжение', description: 'Рабочая группа' }] };
    await render();
    expect(host.querySelector('section')).not.toBeNull();
    await act(async () => host.querySelector('button')!.click());
    expect(requests.find(r => r.body)?.body).toMatchObject({ action: 'choose', template_id: 'registered' });
  });
});
