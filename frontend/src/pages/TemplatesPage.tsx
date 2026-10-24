import { useRef } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { FilePlus2, Files } from 'lucide-react';
import { templatesApi } from '../api/templates';
import { useTemplateCatalog, useTemplateDrafts } from '../hooks/useTemplates';
import { useAuth } from '../features/auth/useAuth';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import { moduleTitle, documentKind, progress, formatTemplateDate } from '../components/templates/templateDisplay';
import { ApiError } from '../api/client';
import type { DocumentTemplate, TemplateDraft } from '../types/templates';

export function TemplateBadge({ template }: { template: Pick<DocumentTemplate, 'schema_version' | 'profile' | 'support_status'> }) {
  return <span className={`template-badge ${template.schema_version === 2 ? 'v2' : ''}`}>{template.support_status === 'needs_configuration' ? 'Требует настройки' : template.schema_version === 2 ? `Проверенный v2 · ${template.profile?.revision ?? '2'}` : 'Legacy v1 · старый режим'}</span>;
}
export function TemplateCard({ template, pending, onCreate }: { template: DocumentTemplate; pending: boolean; onCreate: () => void }) {
  const supported = template.enabled && template.support_status !== 'needs_configuration';
  return <article className="template-card"><Files size={23} /><TemplateBadge template={template} /><h3>{moduleTitle(template)}</h3><p className="muted">{documentKind(template)} · {template.name}</p><p>{template.description || (template.schema_version === 2 ? 'Подготовка проекта по проверенному модулю со структурной обработкой DOCX.' : 'Заполнение по старой схеме. Структурная очистка v2 не предусмотрена.')}</p>
    {!supported && <p className="template-warning">{template.support_status === 'needs_configuration' ? 'Нужен проверенный профиль, связанный с исходным DOCX. Обратитесь к администратору.' : 'Создание временно недоступно.'}</p>}
    <button className="button primary" disabled={!supported || pending} onClick={onCreate}><FilePlus2 size={17} />{pending ? 'Создаём…' : 'Создать документ'}</button></article>;
}
export function DraftProgress({ draft }: { draft: TemplateDraft }) {
  const p = progress(draft);
  return <div className="template-progress"><div><strong>{p.completed} из {p.total} обязательных полей</strong><span>{p.percent}%</span></div><progress value={p.percent} max={100} aria-label="Готовность черновика" /></div>;
}
function DraftCard({ draft }: { draft: TemplateDraft }) {
  const template = useQuery({ queryKey: ['templates', 'detail', draft.template_id], queryFn: ({ signal }) => templatesApi.get(draft.template_id, signal), gcTime: 0 });
  if (template.isError && template.error instanceof ApiError && [401, 403, 404].includes(template.error.status)) return null;
  if (template.isError) return <article className="template-card"><ErrorNotice error={template.error} onRetry={() => void template.refetch()} /></article>;
  return <article className="template-card"><h3>{template.data ? moduleTitle(template.data) : 'Сохранённый проект'}</h3><p className="muted">Создан: {formatTemplateDate(draft.created_at)}<br />Изменён: {formatTemplateDate(draft.updated_at)}</p><DraftProgress draft={draft} /><p>{(draft.validation?.ready ?? draft.status === 'ready_to_generate') ? 'Данные заполнены · доступна проверка' : 'Черновик · продолжите подготовку'}</p><Link className="button secondary" to={`/templates/drafts/${draft.id}`}>Продолжить</Link></article>;
}
export function TemplatesPage() {
  const catalog = useTemplateCatalog(); const drafts = useTemplateDrafts(); const user = useAuth(); const navigate = useNavigate();
  const createLock = useRef(false);
  const create = useMutation({ mutationFn: templatesApi.create, onSuccess: draft => navigate(`/templates/drafts/${draft.id}`) });
  return <div className="templates-page"><div className="page-heading"><div><span className="eyebrow">ПОДГОТОВКА ДОКУМЕНТОВ</span><h1>Шаблоны</h1><p>От проверенного модуля до проекта DOCX — с сохранением ответов и проверкой реквизитов.</p></div>{user.data?.role === 'admin' && <Link to="/templates/register" className="button secondary">Зарегистрировать шаблон</Link>}</div>
    {create.isError && <ErrorNotice error={create.error} />}
    <section aria-label="Каталог шаблонов"><h2>Доступные шаблоны</h2>{catalog.isPending && <Loading />}{catalog.isError ? <ErrorNotice error={catalog.error} onRetry={() => void catalog.refetch()} /> : <div className="template-grid">{catalog.data?.pages.flatMap(page => page.items ?? []).map(template => <TemplateCard key={template.id} template={template} pending={create.isPending} onCreate={() => { if (createLock.current) return; createLock.current = true; create.mutate(template.id, { onSettled: () => { createLock.current = false; } }); }} />)}</div>}
      {catalog.isSuccess && !catalog.data.pages.some(p => p.items?.length) && <p className="template-empty">Доступных шаблонов пока нет. Администратор может зарегистрировать проверенный модуль и предоставить доступ.</p>}
      {catalog.hasNextPage && <button className="button secondary" disabled={catalog.isFetchingNextPage} onClick={() => void catalog.fetchNextPage()}>Показать ещё шаблоны</button>}</section>
    <section aria-label="Мои черновики"><h2>Мои черновики</h2>{drafts.isPending && <Loading />}{drafts.isError ? <ErrorNotice error={drafts.error} onRetry={() => void drafts.refetch()} /> : <div className="template-grid">{drafts.data?.pages.flatMap(page => page.items ?? []).map(draft => <DraftCard key={draft.id} draft={draft} />)}</div>}
      {drafts.isSuccess && !drafts.data.pages.some(p => p.items?.length) && <p className="template-empty">Создайте первый проект. Ответы будут сохранены в вашем личном черновике.</p>}
      {drafts.hasNextPage && <button className="button secondary" disabled={drafts.isFetchingNextPage} onClick={() => void drafts.fetchNextPage()}>Показать ещё черновики</button>}</section>
  </div>;
}
