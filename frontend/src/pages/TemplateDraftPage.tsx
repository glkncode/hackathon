import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '../api/client';
import { templatesApi, validationFromError } from '../api/templates';
import { draftOptions, templateKeys } from '../hooks/useTemplates';
import { authKey } from '../app/providers';
import type { User } from '../types/api';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import { emptyValue, TemplateFieldRenderer } from '../components/templates/TemplateFieldRenderer';
import { ApprovalRoute, SourceNote, ValidationReport } from '../components/templates/ValidationReport';
import { moduleTitle, progress, valueText, visibleFields } from '../components/templates/templateDisplay';
import { DraftProgress, TemplateBadge } from './TemplatesPage';
import type { FieldValue, TemplateDraft, TemplateField, TemplateValidation } from '../types/templates';

function FieldEditor({ field, draft, editing, onSaved, onCancel, onConflict, reload }: {
  field: TemplateField; draft: TemplateDraft; editing: boolean; onSaved: (draft: TemplateDraft) => void;
  onCancel: () => void; onConflict: () => void; reload: () => Promise<TemplateDraft>;
}) {
  const [value, setValue] = useState<FieldValue>(draft.values[field.key] ?? emptyValue(field));
  const [conflict, setConflict] = useState(false); const [refreshing, setRefreshing] = useState(false);
  const [expectedRevision, setExpectedRevision] = useState(draft.revision);
  const [reloadError, setReloadError] = useState<unknown>();
  const lock = useRef(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => { headingRef.current?.focus(); }, [field.key]);
  const visible = !draft.validation?.visible_fields || draft.validation.visible_fields.includes(field.key);
  const required = draft.validation?.required_fields?.includes(field.key) ?? field.required;
  const save = useMutation({
    mutationFn: () => editing ? templatesApi.update(draft.id, expectedRevision, { [field.key]: value }) : templatesApi.answer(draft.id, expectedRevision, field.key, value),
    onSuccess: onSaved,
    onError: error => { if (error instanceof ApiError && error.status === 409) { setConflict(true); onConflict(); } },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault(); if (lock.current || conflict || !visible) return;
    lock.current = true; save.mutate(undefined, { onSettled: () => { lock.current = false; } });
  };
  if (save.error instanceof ApiError && [401, 403, 404].includes(save.error.status)) return <div className="template-panel"><ErrorNotice error={save.error} /><Link to="/templates">К доступным шаблонам</Link></div>;
  return <form className="template-panel template-question" onSubmit={submit}>
    <span className="eyebrow">{editing ? 'ИЗМЕНЕНИЕ ОТВЕТА' : `ШАГ ${Math.min(draft.required_field_count, draft.completed_field_count + 1)} ИЗ ${draft.required_field_count}`}</span>
    <h2 ref={headingRef} tabIndex={-1}>{field.question.replace(/\?+$/, '?')}</h2>
    <TemplateFieldRenderer field={field} value={value} onChange={setValue} required={required} disabled={save.isPending || refreshing} error={save.error instanceof ApiError && [400, 422].includes(save.error.status) ? (field.type === 'date' ? 'Проверьте дату: требуется существующая календарная дата в формате ГГГГ-ММ-ДД.' : 'Ответ не принят. Проверьте заполнение, число строк и допустимую длину.') : undefined} />
    <SourceNote source={field.source} />
    {save.isError && !(save.error instanceof ApiError && save.error.status === 409) && <ErrorNotice error={save.error} message={save.error instanceof ApiError && save.error.status === 400 ? 'Ответ не принят. Проверьте формат даты, заполнение строк и допустимую длину. Ввод сохранён на экране; исправьте и повторите.' : undefined} />}
    {conflict && <div role="alert" className="template-check warning"><strong>Черновик изменён в другом окне.</strong><p>Ваш ввод сохранён на экране. Получите актуальную версию, сравните сохранённый ответ и повторно нажмите «Сохранить», если хотите применить свой ввод.</p><button type="button" className="button secondary" disabled={refreshing} onClick={async () => { setRefreshing(true); setReloadError(undefined); try { const latest = await reload(); setExpectedRevision(latest.revision); setConflict(false); } catch (error) { setReloadError(error); } finally { setRefreshing(false); } }}>Получить актуальный черновик</button></div>}
    {reloadError !== undefined && <ErrorNotice error={reloadError} />}
    {(editing || conflict || expectedRevision !== draft.revision) && <details open={conflict || undefined}><summary>Сейчас сохранено · версия {draft.revision}</summary><p className="template-value">{valueText(field, draft.values[field.key])}</p></details>}
    {!visible && <p role="alert" className="template-warning">После обновления это поле больше не применяется. Ввод остаётся на экране; вернитесь к проверке данных.</p>}
    <div className="template-actions"><button className="button primary" disabled={save.isPending || conflict || refreshing || !visible}>{save.isPending ? 'Сохраняем…' : save.isError && !(save.error instanceof ApiError && save.error.status === 409) ? 'Повторить сохранение' : editing ? 'Сохранить изменение' : 'Сохранить и продолжить'}</button><button type="button" className="button secondary" disabled={save.isPending || refreshing} onClick={onCancel}>К проверке данных</button></div>
    <p className="muted">Для продолжения позже сохраните ответ, затем откройте этот проект в разделе «Мои черновики».</p>
  </form>;
}

export function TemplateDraftPage() {
  const { id = '' } = useParams(); const navigate = useNavigate(); const client = useQueryClient();
  const query = useQuery(draftOptions(id)); const draft = query.data;
  const template = useQuery({ queryKey: ['templates', 'detail', draft?.template_id], queryFn: ({ signal }) => templatesApi.get(draft!.template_id, signal), enabled: !!draft, gcTime: 0 });
  const [review, setReview] = useState(false); const [selected, setSelected] = useState<string | null>(null); const [saved, setSaved] = useState('');
  const generateLock = useRef(false);
  const applyReport = (report: TemplateValidation) => {
    setReview(true);
    client.setQueryData<TemplateDraft>(templateKeys.draft(id), previous => previous ? { ...previous, validation: report } : previous);
  };
  const validate = useMutation({ mutationFn: () => templatesApi.validate(id), onSuccess: applyReport });
  const generate = useMutation({ mutationFn: async () => {
    const report = await templatesApi.validate(id); applyReport(report);
    if (!report.ready || report.checks?.some(check => check.category === 'blocking' && check.result === 'failed')) throw new ApiError(422, report);
    return templatesApi.generate(id, draft!.revision);
  }, onSuccess: result => { if (client.getQueryData<User | null>(authKey)?.id === draft?.owner_id) navigate(`/templates/results/${result.generated_document_id}`); }, onError: error => { const report = validationFromError(error); if (report) applyReport(report); } });
  if (query.isPending) return <div className="templates-page"><Loading label="Загружаем сохранённый черновик…" /></div>;
  if (!draft || query.isError && query.error instanceof ApiError && [401, 403, 404].includes(query.error.status)) return <div className="templates-page"><Link to="/templates" className="text-button">← К шаблонам</Link><ErrorNotice error={query.error} onRetry={() => void query.refetch()} /></div>;
  if (!draft.validation) return <div className="templates-page"><p role="status">Ответ сохранён · версия {draft.revision}</p>{query.isError ? <ErrorNotice error={query.error} onRetry={() => void query.refetch()} /> : <Loading label="Получаем следующий вопрос…" />}</div>;
  if (template.isError) return <div className="templates-page"><ErrorNotice error={template.error} onRetry={() => void template.refetch()} /><Link to="/templates">К доступным шаблонам</Link></div>;
  const report = draft.validation!; const fields = visibleFields(draft);
  const current = selected ? draft.fields.find(field => field.key === selected) : !review ? report.next_question : undefined;
  const editing = selected !== null;
  const busy = query.isFetching || query.isError || validate.isPending || generate.isPending;
  const edit = (key: string) => { setSelected(key); setReview(false); setSaved(''); window.scrollTo({ top: 0, behavior: 'smooth' }); };
  const onSaved = (updated: TemplateDraft) => {
    // A response arriving after logout/account switching must not resurrect
    // private query data that clearSession has already removed.
    if (client.getQueryData<User | null>(authKey)?.id !== updated.owner_id) return;
    client.setQueryData(templateKeys.draft(id), updated); void client.invalidateQueries({ queryKey: ['templates', 'drafts'] });
    setSaved(`Ответ сохранён · версия ${updated.revision}`); setSelected(null); setReview(editing);
    if (!updated.validation) void query.refetch();
  };
  const previous = fields.filter(field => field.key !== current?.key && draft.values[field.key] !== undefined).at(-1);
  return <div className="templates-page template-draft-page"><Link to="/templates" className="text-button">← Шаблоны и мои черновики</Link>
    <div className="page-heading"><div><span className="eyebrow">ЛИЧНЫЙ ПРОЕКТ</span><h1>{template.data ? moduleTitle(template.data) : 'Подготовка документа'}</h1><p>Сохранено: {new Date(draft.updated_at).toLocaleString('ru-RU')} · версия {draft.revision}</p></div><TemplateBadge template={draft} /></div>
    <div className="template-panel"><DraftProgress draft={draft} /><p className="muted">{progress(draft).total ? 'Количество шагов меняется в зависимости от ответов.' : 'Проверьте применимые реквизиты.'}</p></div>
    {saved && <p role="status" className="template-success">{saved}</p>}
    {query.isError && <ErrorNotice error={query.error} onRetry={() => void query.refetch()} />}
    {busy && <Loading label={generate.isPending ? 'Проверяем данные и формируем DOCX…' : 'Обновляем данные…'} />}
    {current ? <><FieldEditor key={current.key} field={current} draft={draft} editing={editing} onSaved={onSaved} onConflict={() => setSelected(current.key)} onCancel={() => { setReview(true); setSelected(null); }} reload={async () => { const result = await query.refetch(); if (result.error) throw result.error; return result.data!; }} />{previous && <button className="text-button" onClick={() => edit(previous.key)}>← Изменить предыдущий ответ: {previous.label}</button>}</> : <>
      <div className="template-panel"><h2>Проверка проекта документа</h2><p>Данные ниже загружены из сохранённого черновика. Проверьте реквизиты перед формированием проекта.</p><div className="template-actions"><button className="button secondary" disabled={busy} onClick={() => validate.mutate()}>Проверить данные</button>{report.next_question && <button className="button secondary" disabled={busy} onClick={() => { setSelected(null); setReview(false); }}>Продолжить вопросы</button>}<button className="button primary" disabled={busy || !report.ready || report.checks?.some(c => c.category === 'blocking' && c.result === 'failed')} onClick={() => { if (generateLock.current) return; generateLock.current = true; generate.mutate(undefined, { onSettled: () => { generateLock.current = false; } }); }}>Сформировать проект DOCX</button></div><p className="muted">Повторная генерация той же сохранённой версии возвращает тот же результат.</p></div>
      {validate.isError && <ErrorNotice error={validate.error} onRetry={() => validate.mutate()} />}{generate.isError && <ErrorNotice error={generate.error} message={generate.error instanceof ApiError && generate.error.status === 409 ? 'Черновик изменён в другом окне. Получите актуальные ответы и проверьте их перед повторной генерацией.' : undefined} onRetry={generate.error instanceof ApiError && generate.error.status === 409 ? () => { void query.refetch(); generate.reset(); } : undefined} />}
      <ValidationReport report={report} fields={draft.fields} profile={draft.profile} onEdit={edit} /><ApprovalRoute report={report} />
      <section className="template-panel"><h2>Данные документа</h2><p className="muted">Необязательные подтверждения также сохраняются через API. Они не заменяют проверку человеком.</p><div className="template-review-fields">{fields.map(field => <article key={field.key}><div className="template-row-heading"><h3>{field.label}{(report.required_fields?.includes(field.key) ?? field.required) ? ' *' : ''}</h3><button className="text-button" disabled={busy} onClick={() => edit(field.key)}>Изменить</button></div><p className="template-value">{valueText(field, draft.values[field.key])}</p></article>)}</div></section>
    </>}
  </div>;
}
