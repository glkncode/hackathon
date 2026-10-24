import { useMutation, useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { templatesApi } from '../api/templates';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import { ApprovalRoute, ValidationReport } from '../components/templates/ValidationReport';
import { formatTemplateDate, moduleTitle } from '../components/templates/templateDisplay';

export function TemplateResultPage() {
  const { id = '' } = useParams();
  const query = useQuery({ queryKey: ['templates', 'result', id], queryFn: ({ signal }) => templatesApi.generated(id, signal), gcTime: 0, staleTime: 0, refetchOnMount: 'always' });
  const result = query.data;
  const template = useQuery({ queryKey: ['templates', 'detail', result?.template_id], queryFn: ({ signal }) => templatesApi.get(result!.template_id, signal), enabled: !!result, gcTime: 0 });
  const download = useMutation({ mutationFn: () => templatesApi.download(id, result!.filename) });
  if (query.isPending) return <div className="templates-page"><Loading /></div>;
  if (query.isError || !result || template.isError) return <div className="templates-page"><ErrorNotice error={query.error ?? template.error} onRetry={() => { void query.refetch(); void template.refetch(); }} /><Link to="/templates">К шаблонам</Link></div>;
  const snapshot = result.generation_snapshot;
  return <div className="templates-page"><Link to="/templates" className="text-button">← Шаблоны и мои черновики</Link><div className="page-heading"><div><span className="eyebrow">DOCX ГОТОВ К СКАЧИВАНИЮ</span><h1>Проект документа сформирован</h1><p>Для официального использования необходимы проверка, согласование и подписание.</p></div></div>
    <section className="template-panel"><h2>{result.filename}</h2><dl className="template-metadata"><dt>Исходный шаблон</dt><dd>{template.data ? moduleTitle(template.data) : 'Загрузка…'}</dd><dt>Создан</dt><dd>{formatTemplateDate(result.created_at)}</dd><dt>Версия черновика</dt><dd>{result.draft_revision}</dd><dt>Профиль</dt><dd>{snapshot ? `v${snapshot.profile.profile_version} · ${snapshot.profile.revision}` : 'Legacy v1'}</dd><dt>Идентификатор результата</dt><dd>{result.id}</dd></dl><div className="template-actions"><button className="button primary" disabled={download.isPending || query.isFetching} onClick={() => download.mutate()}>{download.isPending ? 'Скачиваем…' : 'Скачать DOCX'}</button><Link className="button secondary" to={`/templates/drafts/${result.draft_id}`}>Вернуться к черновику</Link></div><p className="muted">Сохраните ссылку на эту страницу для повторного скачивания. Результат хранит данные версии {result.draft_revision} и не меняется при последующем редактировании черновика.</p>{download.isError && <ErrorNotice error={download.error} onRetry={() => download.mutate()} />}</section>
    {snapshot && <><ValidationReport report={snapshot.validation} fields={snapshot.profile.fields} /><ApprovalRoute report={snapshot.validation} /></>}
  </div>;
}
