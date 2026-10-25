import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ApprovalRoute, ValidationReport, TemplateFieldRenderer, templatesApi, valueText,
  type FieldValue, type TemplateDraft, type TemplateField,
} from '../../features/templates';
import { apiRequest, ApiError } from '../../shared/api/client';
import { ErrorNotice } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './ChatDocumentPanel.module.css';

interface Workflow {
  stage: 'selecting_template' | 'collecting_fields' | 'reviewing' | 'paused' | 'completed' | 'cancelled';
  version: number;
  reviewed_revision: number;
  expected_field?: string;
}
export interface DocumentView {
  enabled: boolean;
  unavailable?: boolean;
  workflow?: Workflow | null;
  draft?: TemplateDraft;
  template_name?: string;
  options?: { id: string; name: string; description: string }[];
  generated?: { id: string; filename: string; draft_revision: number };
}
interface Action {
  action: string;
  template_id?: string;
  field_key?: string;
  values?: Record<string, FieldValue>;
}

export function DocumentDownload({ id }: { id: string }) {
  const metadata = useQuery({
    queryKey: ['chat', 'artifact', id], queryFn: ({ signal }) => templatesApi.generated(id, signal), retry: false,
    staleTime: 0, gcTime: 0, refetchOnMount: 'always',
  });
  const download = useMutation({ mutationFn: () => templatesApi.download(id, metadata.data?.filename ?? 'Проект.docx') });
  if (!metadata.isFetchedAfterMount || !metadata.data || metadata.isError) return null;
  return <div className={styles.file}>
    <span>Проект документа · DOCX · редакция {metadata.data.draft_revision}</span>
    <button className={`${ui.button} ${ui.secondary}`} disabled={download.isPending}
      onClick={() => download.mutate()}>Скачать DOCX</button>
    <Link to={`/templates/drafts/${metadata.data.draft_id}`}>Открыть черновик</Link>
    {download.isError && <ErrorNotice error={download.error} />}
  </div>;
}

export function ChatDocumentPanel({ sessionId, updatedAt, pending, generatedInHistory = [] }: {
  sessionId: string; updatedAt: number; pending: boolean; generatedInHistory?: string[];
}) {
  const client = useQueryClient();
  const path = `/chat/sessions/${encodeURIComponent(sessionId)}/document`;
  const query = useQuery({
    queryKey: ['chat', 'document', sessionId],
    queryFn: ({ signal }) => apiRequest<DocumentView>(path, { signal }), retry: false,
    staleTime: 0, gcTime: 0, refetchOnMount: 'always',
  });
  const refetch = query.refetch;
  useEffect(() => { void refetch(); }, [updatedAt, refetch]);
  const request = useRef<{ signature: string; id: string } | null>(null);
  const lock = useRef(false);
  const action = useMutation({
    mutationFn: async (input: Action) => {
      const payload = { ...input, workflow_version: query.data?.workflow?.version ?? 0, expected_revision: query.data?.draft?.revision ?? 0 };
      const signature = JSON.stringify(payload);
      if (request.current?.signature !== signature) request.current = { signature, id: crypto.randomUUID() };
      return apiRequest(`${path}/actions`, { method: 'POST', body: { ...payload, client_message_id: request.current.id } });
    },
    onSuccess: async () => {
      request.current = null;
      await Promise.all([
        client.invalidateQueries({ queryKey: ['chat', 'history', sessionId] }),
        client.invalidateQueries({ queryKey: ['chat', 'sessions'] }),
        query.refetch(),
      ]);
    },
    onError: async (error) => { if (error instanceof ApiError && error.status === 409) await query.refetch(); },
    onSettled: () => { lock.current = false; },
  });
  const busy = pending || action.isPending || query.isFetching;
  const perform = (input: Action) => { if (busy || lock.current) return; lock.current = true; action.mutate(input); };
  if (query.isError) {
    if (query.error instanceof ApiError && [404, 405].includes(query.error.status)) return null;
    return <ErrorNotice message="Не удалось загрузить состояние подготовки документа. Вы можете продолжать переписку."
      onRetry={() => void query.refetch()} />;
  }
  if (!query.isFetchedAfterMount || !query.data?.enabled) return null;
  if (query.data.unavailable) return <p role="alert">Доступ к шаблону изменён. Подготовка и скачивание недоступны.</p>;
  const { workflow: flow, draft, options, template_name: name } = query.data;
  // Availability is not an active workflow. Only the server can start one.
  if (!flow) return null;
  if (flow.stage === 'paused') return <div className={styles.compact} aria-label="Подготовка документа приостановлена">
    <span>Подготовка документа приостановлена</span>
    <button className={`${ui.button} ${ui.secondary}`} disabled={busy} onClick={() => perform({ action: 'resume' })}>Продолжить</button>
    {draft && <Link to={`/templates/drafts/${draft.id}`}>Открыть в мастере</Link>}
    {action.isError && <ErrorNotice error={action.error} />}
  </div>;
  if (flow.stage === 'completed' || flow.stage === 'cancelled') {
    const generated = query.data.generated;
    // History owns artifact placement; retain a fallback for older histories.
    if (generated && generatedInHistory.includes(generated.id)) return null;
    return <>
      {generated ? <DocumentDownload id={generated.id} /> : draft &&
        <div className={styles.compact}><Link to={`/templates/drafts/${draft.id}`}>Открыть черновик</Link></div>}
    </>;
  }
  if (!['selecting_template', 'collecting_fields', 'reviewing'].includes(flow.stage)) return null;
  const report = draft?.validation;
  const active = draft?.fields.filter(f => !report?.visible_fields || report.visible_fields.includes(f.key)) ?? [];
  const field = active.find(f => f.key === flow?.expected_field) ?? report?.next_question;
  return <section className={styles.panel} aria-label="Подготовка документа">
    <div className={styles.heading}>
      <div><span className={ui.eyebrow}>Подготовка документа</span><h2>{name ?? 'Создать документ в чате'}</h2></div>
      {draft && <Link to={`/templates/drafts/${draft.id}`}>Открыть в мастере</Link>}
    </div>
    {flow.stage === 'selecting_template' && options?.map(option => <button className={styles.option} key={option.id} disabled={busy}
      onClick={() => perform({ action: 'choose', template_id: option.id })}>
      <strong>{option.name}</strong><span>{option.description}</span>
    </button>)}
    {flow?.stage === 'selecting_template' && !options?.length && <p>В доступном каталоге нет зарегистрированных шаблонов.</p>}
    {draft && <>
      <p>Заполнено обязательных полей: {draft.completed_field_count} из {draft.required_field_count}</p>
      <progress max={Math.max(1, draft.required_field_count)} value={draft.completed_field_count} aria-label="Прогресс заполнения" />
      <div className={styles.actions}>
          <button disabled={busy} onClick={() => perform({ action: 'progress' })}>Показать заполнение</button>
          <button disabled={busy} onClick={() => perform({ action: 'validate' })}>Проверить документ</button>
          <button disabled={busy} onClick={() => perform({ action: 'pause' })}>Продолжить позже</button>
          <button disabled={busy} onClick={() => perform({ action: 'cancel' })}>Завершить подготовку</button>
      </div>
      {field && <details open={['repeater', 'choice', 'date', 'person_list', 'text_list'].includes(field.type) || undefined}>
        <summary>Ввести ответ в форме</summary>
        <InlineAnswer key={`${draft.id}:${draft.revision}:${field.key}`} field={field} draft={draft} disabled={busy}
          save={value => perform({ action: 'answer', values: { [field.key]: value } })} />
      </details>}
      <details open={flow.stage === 'reviewing' || undefined}>
        <summary>Данные и результаты проверки</summary>
        <dl>{active.map(f => <div key={f.key}>
          <dt>{f.label}</dt><dd>{valueText(f, draft.values[f.key])}</dd>
          <button disabled={busy} onClick={() => perform({ action: 'edit', field_key: f.key })}>Изменить: {f.label}</button>
        </div>)}</dl>
        {report && <><ValidationReport report={report} fields={draft.fields} profile={draft.profile} /><ApprovalRoute report={report} /></>}
      </details>
      {report?.ready && <>
        <p>Это проект для дальнейшей проверки. Проверьте реквизиты, предупреждения и рекомендации по согласованию.</p>
        <button className={`${ui.button} ${ui.primary}`} disabled={busy}
          onClick={() => perform({ action: 'generate' })}>{action.isPending ? 'Выполняем…' : 'Создать DOCX'}</button>
      </>}
    </>}
    {action.isError && <ErrorNotice error={action.error} onRetry={() => void query.refetch()} />}
  </section>;
}

function InlineAnswer({ field, draft, disabled, save }: { field: TemplateField; draft: TemplateDraft; disabled: boolean; save: (value: FieldValue) => void }) {
  const [value, setValue] = useState<FieldValue>(draft.values[field.key] ?? (['repeater', 'person_list', 'text_list'].includes(field.type) ? [] : ''));
  return <form onSubmit={e => { e.preventDefault(); save(value); }}>
    <TemplateFieldRenderer field={field} value={value} onChange={setValue} disabled={disabled}
      required={draft.validation?.required_fields?.includes(field.key) ?? field.required} />
    <button className={`${ui.button} ${ui.secondary}`} disabled={disabled}>Сохранить ответ</button>
  </form>;
}
