import { Link } from 'react-router-dom';
import templateStyles from '../../features/templates/styles/templates.module.css';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './TemplateRegisterPage.module.css';
import { useTemplateRegistration } from './useTemplateRegistration';

export function TemplateRegisterPage() {
  const {
    user,
    documentId,
    setDocumentId,
    name,
    setName,
    description,
    setDescription,
    profile,
    fileError,
    documents,
    source,
    register,
    needsConfiguration,
    submit,
    readProfile,
  } = useTemplateRegistration();
  if (user.data?.role !== 'admin')
    return (
      <div className={templateStyles['templates-page']}>
        <ErrorNotice message="Регистрация шаблонов доступна администратору." />
        <Link to="/templates">К шаблонам</Link>
      </div>
    );
  return (
    <div className={templateStyles['templates-page']}>
      <Link to="/templates" className={ui['text-button']}>
        ← К шаблонам
      </Link>
      <div className={ui['page-heading']}>
        <div>
          <span className={ui['eyebrow']}>Администрирование</span>
          <h1>Регистрация проверенного шаблона</h1>
          <p>
            Выберите исходный DOCX и подготовленный профиль v2, соответствующий его точной версии.
          </p>
        </div>
      </div>
      <form
        className={cx(templateStyles['template-panel'], styles['template-register'])}
        onSubmit={submit}
      >
        <p>
          Сначала загрузите оригинальный модуль в{' '}
          <Link className={ui['text-button']} to="/documents">
            базу знаний
          </Link>
          . Загрузите проверенный JSON-профиль для выбранного шаблона. Сервер проверит схему,
          SHA-256 и структурные привязки; произвольный DOCX не становится поддерживаемым
          автоматически.
        </p>
        {documents.isPending && <Loading />}
        {documents.isError && (
          <ErrorNotice error={documents.error} onRetry={() => void documents.refetch()} />
        )}
        <label>
          Исходный документ
          <select required value={documentId} onChange={(e) => setDocumentId(e.target.value)}>
            <option value="">Выберите документ</option>
            {documents.data?.pages
              .flatMap((p) => p.items ?? [])
              .map((doc) => (
                <option value={doc.id} key={doc.id}>
                  {doc.title}
                </option>
              ))}
          </select>
        </label>
        {documents.hasNextPage && (
          <button
            type="button"
            className={ui['text-button']}
            disabled={documents.isFetchingNextPage}
            onClick={() => void documents.fetchNextPage()}
          >
            Загрузить ещё документы
          </button>
        )}
        {source.isPending && documentId && <Loading />}
        {source.isError && <ErrorNotice error={source.error} />}
        {source.data && (
          <p className={ui['muted']}>
            Фиксируемая версия: {source.data.latest_version.id}
            <br />
            Файл: {source.data.latest_version.original_filename}
            <br />
            SHA-256: {source.data.latest_version.sha256 ?? 'проверяется сервером'}
          </p>
        )}
        <label>
          Название шаблона
          <input required maxLength={255} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          Описание
          <textarea
            maxLength={2000}
            rows={3}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        <label>
          Проверенный профиль v2 (.json)
          <input type="file" required accept=".json,application/json" onChange={readProfile} />
        </label>
        {fileError && <ErrorNotice message={fileError} />}
        {profile && (
          <p className={templateStyles['template-success']}>
            Профиль {profile.profile_id} · {profile.revision} · {profile.fields.length} полей.
            Окончательная проверка выполняется сервером.
          </p>
        )}
        {register.isError && (
          <ErrorNotice
            error={register.error}
            message={
              needsConfiguration
                ? 'Требуется настройка: для этого DOCX нет подходящей схемы. Подготовьте проверенный профиль, соответствующий оригиналу.'
                : undefined
            }
          />
        )}
        <button
          className={cx(ui['button'], ui['primary'])}
          disabled={!profile || !source.data || source.isFetching || register.isPending}
        >
          {register.isPending ? 'Регистрируем…' : 'Зарегистрировать шаблон'}
        </button>
        <p className={ui['muted']}>
          Регистрация фиксирует исходную версию и профиль. Для изменения профиля создаётся новая
          регистрация.
        </p>
      </form>
    </div>
  );
}
