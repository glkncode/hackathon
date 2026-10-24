// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/client';
import { templatesApi } from '../api/templates';
import { documentsApi } from '../api/documents';
import { clearSession, authKey } from '../app/providers';
import { TemplateDraftPage } from './TemplateDraftPage';
import { TemplateResultPage } from './TemplateResultPage';
import { TemplateRegisterPage } from './TemplateRegisterPage';
import { TemplatesPage, TemplateCard } from './TemplatesPage';
import { TemplateFieldRenderer } from '../components/templates/TemplateFieldRenderer';
import { ApprovalRoute, ValidationReport } from '../components/templates/ValidationReport';
import { loadTemplateDraft } from '../hooks/useTemplates';
import { ruleFields } from '../components/templates/templateDisplay';
import type { DocumentTemplate, FieldValue, TemplateDraft, TemplateField, TemplateValidation } from '../types/templates';

const field: TemplateField = { key: 'subject', label: 'Тема', question: 'Какова тема?', type: 'text', required: true, max_length: 2000, description: '' };
const date: TemplateField = { ...field, key: 'date', label: 'Дата ввода', question: 'Дата ввода?', type: 'date' };
const choice: TemplateField = { ...field, key: 'repeal', label: 'Отменить прежнее распоряжение?', question: 'Отменить прежнее распоряжение?', type: 'choice', options: ['yes', 'no'] };
const extra: TemplateField = { ...field, key: 'old_number', label: 'Номер прежнего распоряжения', question: 'Номер прежнего распоряжения?' };
const source = { name: 'Матрица визирования', section: '2.6', version: '1' };
const report: TemplateValidation = { ready: false, missing_fields: [field], invalid_fields: [], next_question: field, visible_fields: [field.key, choice.key], required_fields: [field.key] };
const template: DocumentTemplate = { id: 'template', document_id: 'source', document_version_id: 'version', name: 'Мой модуль', description: 'Описание', enabled: true, fields: [field], schema_version: 2, profile: { profile_version: 2, profile_id: 'working-group', revision: '2.0.2', rules_version: 'rules', engine_version: 'engine', template_kind: 'order', fields: [field] }, created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z' };
const initial: TemplateDraft = { id: 'draft', template_id: 'template', document_version_id: 'version', owner_id: 'user', values: {}, status: 'draft', revision: 1, fields: [field, choice, extra, date], schema_version: 2, profile: template.profile, validation: report, required_field_count: 1, completed_field_count: 0, created_at: template.created_at, updated_at: template.updated_at };
let server: TemplateDraft;
let host: HTMLDivElement; let root: Root; let client: QueryClient;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true); vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  host = document.createElement('div'); document.body.append(host); root = createRoot(host);
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  // Production keeps this query observed by ProtectedRoute. These isolated
  // page tests retain the authenticated identity without mounting that route.
  client.setQueryDefaults(authKey, { gcTime: Infinity, staleTime: Infinity });
  client.setQueryData(authKey, { id: 'user', username: 'tester', role: 'member' });
  server = structuredClone(initial);
  vi.spyOn(templatesApi, 'draft').mockImplementation(async () => structuredClone(server));
  vi.spyOn(templatesApi, 'next').mockImplementation(async () => structuredClone(server.validation!));
  vi.spyOn(templatesApi, 'get').mockResolvedValue(template);
  vi.spyOn(templatesApi, 'list').mockResolvedValue({ items: [template], limit: 20, offset: 0 });
  vi.spyOn(templatesApi, 'drafts').mockResolvedValue({ items: [], limit: 20, offset: 0 });
});
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function render(element: React.ReactNode, path = '/templates/drafts/draft') {
  await act(async () => root.render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}><Routes><Route path="/templates/drafts/:id" element={element} /><Route path="/templates" element={element} /><Route path="/templates/results/:id" element={<TemplateResultPage />} /></Routes></MemoryRouter></QueryClientProvider>));
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)); });
}
function button(text: string) { const node = [...host.querySelectorAll('button')].find(b => b.textContent === text); if (!node) throw new Error(`Missing button ${text}: ${host.textContent}`); return node; }
async function click(text: string) { await act(async () => button(text).click()); await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function input(value: string, selector = 'input') { const element = host.querySelector<HTMLInputElement>(selector)!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(element, value); element.dispatchEvent(new Event('input', { bubbles: true })); }); }
async function submit() { await act(async () => { host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); }); await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }

describe('catalog and access', () => {
  it('loads real catalog API data, v2 label and creates a draft', async () => {
    vi.spyOn(templatesApi, 'create').mockResolvedValue(server);
    await render(<TemplatesPage />, '/templates');
    expect(host.textContent).toContain('Создание рабочей группы'); expect(host.textContent).toContain('Проверенный v2');
    await click('Создать документ'); expect(templatesApi.create).toHaveBeenCalledWith('template', expect.anything());
  });
  it('distinguishes legacy v1 without promising cleanup', async () => {
    await render(<TemplateCard template={{ ...template, schema_version: 1, profile: undefined, description: '' }} pending={false} onCreate={() => {}} />);
    expect(host.textContent).toContain('Legacy v1'); expect(host.textContent).toContain('Структурная очистка v2 не предусмотрена');
  });
  it.each([{ support_status: 'needs_configuration' as const }, { enabled: false }])('disables unavailable creation %j', async patch => {
    await render(<TemplateCard template={{ ...template, ...patch }} pending={false} onCreate={() => {}} />);
    expect(button('Создать документ').disabled).toBe(true);
  });
  it('renders only API supplied accessible entries', async () => {
    vi.mocked(templatesApi.list).mockResolvedValue({ items: [], limit: 20, offset: 0 });
    await render(<TemplatesPage />, '/templates'); expect(host.textContent).toContain('Доступных шаблонов пока нет'); expect(host.textContent).not.toContain('Создание рабочей группы');
  });
  it('clears template data and mutations with logout/session expiration', async () => {
    client.setQueryData(['templates', 'draft', 'draft'], server); client.setQueryData(['templates', 'result', 'private'], { filename: 'private.docx' });
    await clearSession(client); expect(client.getQueryData(['templates', 'draft', 'draft'])).toBeUndefined(); expect(client.getQueryData(['templates', 'result', 'private'])).toBeUndefined(); expect(client.getQueryData(authKey)).toBeNull();
  });
});
describe('server driven wizard and concurrency', () => {
  it('resumes persisted revision, value and server next question after mount', async () => {
    server = { ...server, revision: 8, values: { subject: 'Сохранено' }, validation: { ...report, next_question: choice, missing_fields: [choice] } };
    await render(<TemplateDraftPage />); expect(host.textContent).toContain('версия 8'); expect(host.textContent).toContain(choice.question); expect(templatesApi.next).not.toHaveBeenCalled();
  });
  it('saves exact revision and follows changed conditional metadata and progress', async () => {
    vi.spyOn(templatesApi, 'answer').mockImplementation(async () => { server = { ...server, revision: 2, completed_field_count: 1, required_field_count: 2, values: { subject: 'Новая тема' }, validation: { ...report, next_question: extra, missing_fields: [extra], visible_fields: ['subject', 'old_number'], required_fields: ['subject', 'old_number'] } }; return server; });
    await render(<TemplateDraftPage />); await input('Новая тема'); await submit();
    expect(templatesApi.answer).toHaveBeenCalledWith('draft', 1, 'subject', 'Новая тема'); expect(host.textContent).toContain(extra.question); expect(host.textContent).toContain('1 из 2'); expect(host.textContent).toContain('50%');
  });
  it('shows only applicable review fields and permits editing saved values', async () => {
    server = { ...server, values: { subject: 'Сохранено', old_number: 'Скрыто' }, validation: { ...report, ready: true, next_question: null, missing_fields: [] } };
    vi.spyOn(templatesApi, 'update').mockImplementation(async () => ({ ...server, revision: 2 }));
    await render(<TemplateDraftPage />); expect(host.textContent).not.toContain('Скрыто');
    await click('Изменить'); await input('Изменено'); await submit(); expect(templatesApi.update).toHaveBeenCalledWith('draft', 1, { subject: 'Изменено' });
  });
  it('preserves local input on 409 and requires explicit refresh before retry', async () => {
    const answer = vi.spyOn(templatesApi, 'answer').mockRejectedValueOnce(new ApiError(409)).mockImplementation(async () => server);
    await render(<TemplateDraftPage />); await input('Мой ответ'); await submit(); expect(host.querySelector('input')!.value).toBe('Мой ответ'); expect(button('Сохранить изменение').disabled).toBe(true);
    server = { ...server, revision: 3, values: { subject: 'Чужое изменение' } };
    await click('Получить актуальный черновик'); expect(host.querySelector('input')!.value).toBe('Мой ответ');
    vi.spyOn(templatesApi, 'update').mockResolvedValue(server);
    await submit(); expect(answer).toHaveBeenCalledTimes(1); expect(templatesApi.update).toHaveBeenCalledWith('draft', 3, { subject: 'Мой ответ' });
  });
  it('does not silently adopt a revision received during background refresh', async () => {
    vi.spyOn(templatesApi, 'answer').mockRejectedValue(new ApiError(409));
    await render(<TemplateDraftPage />); await input('Не потерять');
    await act(async () => { client.setQueryData(['templates', 'draft', 'draft'], { ...server, revision: 99 }); });
    await submit(); expect(templatesApi.answer).toHaveBeenCalledWith('draft', 1, 'subject', 'Не потерять');
  });
  it.each([new TypeError('network'), new ApiError(400), new ApiError(502)])('retains input for rejected save: %s', async error => {
    vi.spyOn(templatesApi, 'answer').mockRejectedValue(error); await render(<TemplateDraftPage />); await input('Не потерять'); await submit(); expect(host.querySelector('input')!.value).toBe('Не потерять'); expect(host.querySelector('[role="alert"]')).not.toBeNull(); expect(button('Повторить сохранение').disabled).toBe(false);
  });
  it('blocks repeated saves while a mutation is pending', async () => {
    vi.spyOn(templatesApi, 'answer').mockImplementation(() => new Promise(() => {})); await render(<TemplateDraftPage />); await input('Тема'); await submit(); await submit(); expect(templatesApi.answer).toHaveBeenCalledTimes(1);
  });
  it('hides private data when draft access is revoked', async () => {
    await render(<TemplateDraftPage />); vi.mocked(templatesApi.draft).mockRejectedValue(new ApiError(404));
    await act(async () => { await client.refetchQueries({ queryKey: ['templates', 'draft', 'draft'] }); });
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)); });
    expect(host.textContent).toContain('больше недоступны'); expect(host.querySelector('form')).toBeNull();
  });
  it('works with v1 missing validation on draft by reading next-question endpoint', async () => {
    server = { ...server, schema_version: 1, profile: undefined };
    vi.mocked(templatesApi.draft).mockImplementation(async () => ({ ...structuredClone(server), validation: undefined }));
    await render(<TemplateDraftPage />); expect(host.textContent).toContain('Legacy v1'); expect(host.textContent).toContain(field.question); expect(templatesApi.next).toHaveBeenCalled();
  });
});

describe('revision consistency regressions', () => {
  it('does not restore private draft data when a pending save completes after logout', async () => {
    let finish!: (draft: TemplateDraft) => void;
    vi.spyOn(templatesApi, 'answer').mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    await render(<TemplateDraftPage />); await input('Ответ до выхода'); await submit();
    await act(async () => root.render(<p>Вышли</p>)); await clearSession(client);
    await act(async () => finish({ ...server, revision: 2 }));
    expect(client.getQueryData(['templates', 'draft', 'draft'])).toBeUndefined();
  });
  it('keeps the validation attached to a v2 revision without a second request', async () => {
    vi.mocked(templatesApi.next).mockRejectedValue(new ApiError(502));
    expect((await loadTemplateDraft('draft')).validation).toEqual(report);
    expect(templatesApi.next).not.toHaveBeenCalled();
  });
  it('does not turn an acknowledged v2 write into a failure when next-question is down', async () => {
    vi.spyOn(templatesApi, 'answer').mockResolvedValue({ ...server, revision: 2, values: { subject: 'Записано' }, validation: { ...report, next_question: choice } });
    vi.mocked(templatesApi.next).mockRejectedValue(new ApiError(502));
    await render(<TemplateDraftPage />); await input('Записано'); await submit();
    expect(host.textContent).toContain('Ответ сохранён · версия 2'); expect(host.textContent).toContain(choice.question);
  });
  it('preserves the edited field after conflict refresh advances the next question', async () => {
    vi.spyOn(templatesApi, 'answer').mockRejectedValue(new ApiError(409));
    await render(<TemplateDraftPage />); await input('Моя тема'); await submit();
    server = { ...server, revision: 4, values: { subject: 'Другая тема' }, validation: { ...report, next_question: choice } };
    await click('Получить актуальный черновик');
    expect(host.querySelector('input')?.value).toBe('Моя тема'); expect(host.textContent).toContain('Другая тема');
  });
  it('retries inconsistent v1 reads and pairs the next question with a stable revision', async () => {
    vi.mocked(templatesApi.draft).mockResolvedValueOnce({ ...server, validation: undefined }).mockResolvedValue({ ...server, revision: 2, validation: undefined });
    expect((await loadTemplateDraft('draft')).revision).toBe(2); expect(templatesApi.next).toHaveBeenCalledTimes(2);
  });
  it('shows a field error for a rejected date and keeps the entered value', async () => {
    server.validation = { ...report, next_question: date, visible_fields: ['date'], required_fields: ['date'] };
    vi.spyOn(templatesApi, 'answer').mockRejectedValue(new ApiError(400));
    await render(<TemplateDraftPage />); await input('2026-12-01'); await submit();
    expect(host.querySelector('input')?.value).toBe('2026-12-01'); expect(host.querySelector('input')?.getAttribute('aria-invalid')).toBe('true'); expect(host.textContent).toContain('существующая календарная дата');
  });
  it.each([403, 404])('removes the editor after write access fails with %s', async status => {
    vi.spyOn(templatesApi, 'answer').mockRejectedValue(new ApiError(status));
    await render(<TemplateDraftPage />); await input('Приватный ввод'); await submit();
    expect(host.querySelector('input')).toBeNull(); expect(host.textContent).not.toContain('Приватный ввод');
  });
  it('reports a renderer outage on initial draft load with a retry action', async () => {
    vi.mocked(templatesApi.draft).mockRejectedValue(new ApiError(502));
    await render(<TemplateDraftPage />); expect(host.querySelector('[role="alert"]')).not.toBeNull(); expect(host.querySelector('form')).toBeNull();
  });
  it('finds related fields from declarative rule metadata without evaluating rules', async () => {
    const profile = { ...template.profile!, rules: [{ id: 'date', check: { op: 'or', conditions: [{ op: 'date_gte', field: 'date', other_field: 'subject' }] } }] };
    expect(ruleFields(profile, 'date')).toEqual(['date', 'subject']);
    const edit = vi.fn();
    await render(<ValidationReport fields={[field, date, extra]} profile={profile} onEdit={edit} report={{ ...report, visible_fields: ['subject', 'date', 'old_number'], checks: [{ rule_id: 'date', category: 'blocking', result: 'failed', source, method: 'declarative', description: 'Исправьте дату' }] }} />);
    await click('Исправить: Дата ввода'); expect(edit).toHaveBeenCalledWith('date'); expect(host.textContent).not.toContain('Номер прежнего распоряжения');
  });
});
describe('field types', () => {
  it('uses ISO date without timezone conversion', async () => {
    const change = vi.fn(); await render(<TemplateFieldRenderer field={date} value="2026-12-01" onChange={change} required />); expect(host.querySelector('input')?.type).toBe('date'); await input('2026-12-15'); expect(change).toHaveBeenCalledWith('2026-12-15');
  });
  it('handles native date input events even when React change tracking already saw the value', async () => {
    const change = vi.fn(); await render(<TemplateFieldRenderer field={date} value="" onChange={change} required />);
    const control = host.querySelector('input')!;
    await act(async () => { control.value = '2026-12-15'; control.dispatchEvent(new Event('input', { bubbles: true })); });
    expect(change).toHaveBeenCalledWith('2026-12-15');
  });
  it('renders exact enum values with Russian labels', async () => {
    const change = vi.fn(); await render(<TemplateFieldRenderer field={choice} value="no" onChange={change} required />); expect(host.querySelector('select')?.value).toBe('no'); expect(host.textContent).toContain('Нет');
  });
  it('edits structured repeater rows, order and delete without stringifying', async () => {
    const repeater: TemplateField = { ...field, type: 'repeater', minimum_items: 1, maximum_items: 3, item_fields: [{ key: 'name', label: 'ФИО', required: true }, { key: 'position', label: 'Должность', required: true }] };
    const change = vi.fn(); const value: FieldValue = [{ name: 'Первый', position: 'Эксперт' }, { name: 'Второй', position: 'Юрист' }];
    await render(<TemplateFieldRenderer field={repeater} value={value} onChange={change} required />); await input('Третий'); expect(change).toHaveBeenCalledWith([{ name: 'Третий', position: 'Эксперт' }, { name: 'Второй', position: 'Юрист' }]);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Опустить строку 1"]')!.click()); expect(change).toHaveBeenLastCalledWith([value[1], value[0]]);
    await click('Удалить строку 1'); expect(change).toHaveBeenLastCalledWith([value[1]]);
    await click('Добавить участника'); expect(change).toHaveBeenLastCalledWith([...value, { name: '', position: '' }]);
  });
  it.each(['person_list', 'text_list'] as const)('supports ordered %s arrays', async type => {
    const change = vi.fn(); await render(<TemplateFieldRenderer field={{ ...field, type }} value={['Один']} onChange={change} required />); await input('Два'); expect(change).toHaveBeenCalledWith(['Два']);
  });
});
describe('review, generation and report', () => {
  function ready() { server = { ...server, values: { subject: 'Тема' }, completed_field_count: 1, validation: { ...report, ready: true, missing_fields: [], next_question: null } }; }
  it('shows semantic blocking with no next question and a route to edit', async () => {
    ready(); server.validation = { ...server.validation!, ready: false, checks: [{ rule_id: 'date', category: 'blocking', result: 'failed', source, method: 'declarative', description: 'Дата ввода раньше готовности' }] };
    await render(<TemplateDraftPage />); expect(host.textContent).toContain('Дата ввода раньше готовности'); expect(button('Сформировать проект DOCX').disabled).toBe(true); await click('Изменить'); expect(host.querySelector('form')).not.toBeNull();
  });
  it('displays warning/manual/info sources and object route applicability safely', async () => {
    const detailed: TemplateValidation = { ...report, checks: ['warning', 'manual_review', 'info'].map(category => ({ rule_id: category, category: category as 'warning', result: category === 'manual_review' ? 'user_confirmed' : 'attention', source, method: 'manual', description: `Проверка ${category}` })), approval_route: { document_kind: 'order', note: 'Нужна проверка', status: 'recommendation', required_departments: ['ДОУ'], additional_departments: ['Юристы'], conditional_departments: [{ name: 'ИТ', condition: 'При ИТ-проекте', applicable: true, row_id: '1', source }], rows: [{ row_id: '1', description: 'Рабочая группа', applicable: true, applicability_condition: { op: 'eq', field: 'it', value: 'yes' }, required_departments: ['ДОУ'], source }] } };
    await render(<><ValidationReport report={detailed} /><ApprovalRoute report={detailed} /></>); expect(host.textContent).toContain('независимой проверки'); expect(host.textContent).toContain('Рекомендуемый маршрут'); expect(host.textContent).toContain('Матрица визирования'); expect(host.textContent).toContain('При ИТ-проекте');
  });
  it('never labels manual review as a passed check, even if its result says passed', async () => {
    await render(<ValidationReport report={{ ...report, checks: [{ rule_id: 'manual', category: 'manual_review', result: 'passed', source, method: 'manual', description: 'Проверьте полномочия' }] }} />);
    expect(host.textContent).toContain('Необходима проверка человеком'); expect(host.textContent).not.toContain('Условие выполнено'); expect(host.querySelector('.manual_review.passed')).toBeNull();
  });
  it('validates immediately before generation and navigates to persisted result', async () => {
    ready(); vi.spyOn(templatesApi, 'validate').mockResolvedValue(server.validation!); vi.spyOn(templatesApi, 'generate').mockResolvedValue({ generated_document_id: 'result', draft_id: 'draft', draft_revision: 1, filename: 'result.docx', status: 'ready', download_url: '/api/v1/generated-documents/result/download' });
    vi.spyOn(templatesApi, 'generated').mockResolvedValue({ id: 'result', draft_id: 'draft', template_id: 'template', document_version_id: 'version', draft_revision: 1, filename: 'result.docx', mime_type: '', sha256: '', size_bytes: 100, created_at: template.created_at });
    await render(<TemplateDraftPage />); await click('Сформировать проект DOCX'); expect(templatesApi.validate).toHaveBeenCalled(); expect(templatesApi.generate).toHaveBeenCalledWith('draft', 1); expect(host.textContent).toContain('Проект документа сформирован'); expect(host.textContent).toContain('result.docx');
  });
  it('prevents generation when fresh validation returns not-ready', async () => {
    ready(); vi.spyOn(templatesApi, 'validate').mockResolvedValue(report); vi.spyOn(templatesApi, 'generate'); await render(<TemplateDraftPage />); await click('Сформировать проект DOCX'); expect(templatesApi.generate).not.toHaveBeenCalled(); expect(host.textContent).toContain('Обязательные поля');
  });
  it('preserves structured 422 checks returned by generate', async () => {
    ready(); vi.spyOn(templatesApi, 'validate').mockResolvedValue(server.validation!); vi.spyOn(templatesApi, 'generate').mockRejectedValue(new ApiError(422, { ...report, checks: [{ rule_id: 'date', category: 'blocking', result: 'failed', source, method: 'declarative', description: 'Исправьте даты' }] }));
    await render(<TemplateDraftPage />); await click('Сформировать проект DOCX'); expect(host.textContent).toContain('Исправьте даты'); expect(button('Сформировать проект DOCX').disabled).toBe(true);
  });
  it('reloads existing result metadata and can download again', async () => {
    vi.spyOn(templatesApi, 'generated').mockResolvedValue({ id: 'result', draft_id: 'draft', template_id: 'template', document_version_id: 'version', draft_revision: 5, filename: 'old.docx', mime_type: '', sha256: '', size_bytes: 100, created_at: template.created_at }); vi.spyOn(templatesApi, 'download').mockResolvedValue();
    await render(<TemplateResultPage />, '/templates/results/result'); await click('Скачать DOCX'); expect(templatesApi.download).toHaveBeenCalledWith('result', 'old.docx'); expect(host.textContent).toContain('версии 5');
  });
});

describe('verified profile registration', () => {
  it('keeps admin actions and source document requests out of the member scenario', async () => {
    vi.spyOn(documentsApi, 'list'); await render(<TemplateRegisterPage />, '/templates');
    expect(host.textContent).toContain('доступна администратору'); expect(documentsApi.list).not.toHaveBeenCalled();
  });
  it('submits the exact uploaded profile with the selected pinned document version', async () => {
    client.setQueryData(authKey, { id: 'admin', username: 'admin', role: 'admin' });
    const record = { id: 'source', title: 'Проверенный оригинал', created_by: 'admin', active_version_id: 'version', created_at: template.created_at, updated_at: template.updated_at };
    const version = { id: 'version', document_id: 'source', version_number: 1, original_filename: 'module.docx', mime_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', size_bytes: 100, created_at: template.created_at, updated_at: template.updated_at, status: 'ready' as const, ingestion_job_id: null };
    vi.spyOn(documentsApi, 'list').mockResolvedValue({ items: [record], total: 1 });
    vi.spyOn(documentsApi, 'get').mockResolvedValue({ ...record, latest_version: version, active_version: version });
    vi.spyOn(templatesApi, 'register').mockRejectedValue(new ApiError(400, { support_status: 'needs_configuration' }));
    await render(<TemplateRegisterPage />, '/templates');
    await act(async () => { const select = host.querySelector('select')!; select.value = 'source'; select.dispatchEvent(new Event('change', { bubbles: true })); });
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)); });
    await input('Проверенный модуль', 'input:not([type="file"])');
    const file = new File(['{}'], 'working-group.json', { type: 'application/json' });
    Object.defineProperty(file, 'text', { value: async () => JSON.stringify(template.profile) });
    await act(async () => { const control = host.querySelector('input[type="file"]')!; Object.defineProperty(control, 'files', { value: [file] }); control.dispatchEvent(new Event('change', { bubbles: true })); });
    await submit();
    expect(templatesApi.register).toHaveBeenCalledWith({ document_id: 'source', document_version_id: 'version', name: 'Проверенный модуль', description: '', profile: template.profile });
    expect(host.textContent).toContain('Требуется настройка');
  });
});
