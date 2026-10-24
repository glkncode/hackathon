import { useRef, useState, type FormEvent } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { documentsApi } from '../api/documents';
import { templatesApi } from '../api/templates';
import { ApiError } from '../api/client';
import { useAuth } from '../features/auth/useAuth';
import { ErrorNotice, Loading } from '../components/ui/Feedback';
import type { TemplateProfile } from '../types/templates';

export function TemplateRegisterPage() {
  const user = useAuth(); const navigate = useNavigate(); const client = useQueryClient();
  const [documentId, setDocumentId] = useState(''); const [name, setName] = useState(''); const [description, setDescription] = useState('');
  const [profile, setProfile] = useState<TemplateProfile>(); const [fileError, setFileError] = useState('');
  const registerLock = useRef(false);
  const documents = useInfiniteQuery({ queryKey: ['template-source-documents'], initialPageParam: 0, queryFn: ({ pageParam, signal }) => documentsApi.list(pageParam, signal), getNextPageParam: (p, pages) => p.items?.length === 20 ? pages.length * 20 : undefined, enabled: user.data?.role === 'admin' });
  const source = useQuery({ queryKey: ['template-source-document', documentId], queryFn: ({ signal }) => documentsApi.get(documentId, signal), enabled: !!documentId && user.data?.role === 'admin' });
  const register = useMutation({ mutationFn: () => templatesApi.register({ document_id: documentId, document_version_id: source.data!.latest_version.id, name, description, profile: profile! }), onSuccess: async () => { await client.invalidateQueries({ queryKey: ['templates'] }); navigate('/templates'); } });
  if (user.data?.role !== 'admin') return <div className="templates-page"><ErrorNotice message="Регистрация шаблонов доступна администратору." /><Link to="/templates">К шаблонам</Link></div>;
  const needsConfiguration = register.error instanceof ApiError && typeof register.error.details === 'object' && register.error.details !== null && 'support_status' in register.error.details && register.error.details.support_status === 'needs_configuration';
  const submit = (event: FormEvent) => { event.preventDefault(); if (!registerLock.current && !register.isPending && profile && source.data && !source.isFetching) { registerLock.current = true; register.mutate(undefined, { onSettled: () => { registerLock.current = false; } }); } };
  return <div className="templates-page"><Link to="/templates" className="text-button">← К шаблонам</Link><div className="page-heading"><div><span className="eyebrow">АДМИНИСТРИРОВАНИЕ</span><h1>Регистрация проверенного шаблона</h1><p>Выберите исходный DOCX и подготовленный профиль v2, соответствующий его точной версии.</p></div></div>
    <form className="template-panel template-register" onSubmit={submit}><p>Сначала загрузите оригинальный модуль в <Link className="text-button" to="/documents">базу знаний</Link>. Проверенные JSON-профили находятся в docs/template-profiles/v2. Сервер проверяет схему, SHA-256 и структурные привязки; произвольный DOCX не становится поддерживаемым автоматически.</p>
      {documents.isPending && <Loading />}{documents.isError && <ErrorNotice error={documents.error} onRetry={() => void documents.refetch()} />}
      <label>Исходный документ<select required value={documentId} onChange={e => setDocumentId(e.target.value)}><option value="">Выберите документ</option>{documents.data?.pages.flatMap(p => p.items ?? []).map(doc => <option value={doc.id} key={doc.id}>{doc.title}</option>)}</select></label>
      {documents.hasNextPage && <button type="button" className="text-button" disabled={documents.isFetchingNextPage} onClick={() => void documents.fetchNextPage()}>Загрузить ещё документы</button>}
      {source.isPending && documentId && <Loading />}{source.isError && <ErrorNotice error={source.error} />}
      {source.data && <p className="muted">Фиксируемая версия: {source.data.latest_version.id}<br />Файл: {source.data.latest_version.original_filename}<br />SHA-256: {source.data.latest_version.sha256 ?? 'проверяется сервером'}</p>}
      <label>Название шаблона<input required maxLength={255} value={name} onChange={e => setName(e.target.value)} /></label>
      <label>Описание<textarea maxLength={2000} rows={3} value={description} onChange={e => setDescription(e.target.value)} /></label>
      <label>Проверенный профиль v2 (.json)<input type="file" required accept=".json,application/json" onChange={async e => {
        setProfile(undefined); setFileError(''); const file = e.target.files?.[0]; if (!file) return;
        try { if (file.size > 196608) throw new Error(); const parsed: unknown = JSON.parse(await file.text());
          if (!parsed || typeof parsed !== 'object' || !('profile_version' in parsed) || parsed.profile_version !== 2 || !('fields' in parsed) || !Array.isArray(parsed.fields)) throw new Error();
          setProfile(parsed as TemplateProfile);
        } catch { setFileError('Не удалось прочитать профиль v2. Выберите подготовленный JSON-файл размером до 192 КиБ.'); }
      }} /></label>
      {fileError && <ErrorNotice message={fileError} />}{profile && <p className="template-success">Профиль {profile.profile_id} · {profile.revision} · {profile.fields.length} полей. Окончательная проверка выполняется сервером.</p>}
      {register.isError && <ErrorNotice error={register.error} message={needsConfiguration ? 'Требуется настройка: для этого DOCX нет подходящей схемы. Подготовьте проверенный профиль, соответствующий оригиналу.' : undefined} />}
      <button className="button primary" disabled={!profile || !source.data || source.isFetching || register.isPending}>{register.isPending ? 'Регистрируем…' : 'Зарегистрировать шаблон'}</button><p className="muted">Регистрация фиксирует исходную версию и профиль. Для изменения профиля создаётся новая регистрация.</p>
    </form></div>;
}
