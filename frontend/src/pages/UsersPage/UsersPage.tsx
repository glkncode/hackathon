import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, UserPlus, Users } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useAuth } from '../../features/auth';
import { documentsApi } from '../../features/documents';
import { accessKey, usersApi, usersKey, type ListedUser } from '../../features/users';
import { ApiError, errorMessage } from '../../shared/api/client';
import type { Collection } from '../../shared/types/collection';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './UsersPage.module.css';

function creationError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 409) return 'Этот логин уже занят. Выберите другой.';
    if (error.status === 400) return 'Проверьте логин и пароль: логин — 3–64 символа A–Z, a–z, 0–9, _, -, .; пароль — от 12 символов, до 1024 байт UTF-8.';
  }
  return errorMessage(error);
}

function CreateUser({ onCreated }: { onCreated: (user: ListedUser) => void }) {
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [validation, setValidation] = useState('');
  const create = useMutation({
    // Keep the password out of React state and mutation variables.
    mutationFn: () => {
      const username = form.current!.elements.namedItem('username') as HTMLInputElement;
      const password = form.current!.elements.namedItem('password') as HTMLInputElement;
      const request = usersApi.create(username.value, password.value);
      password.value = '';
      return request;
    },
    onSuccess: async (user) => {
      form.current?.reset();
      await client.invalidateQueries({ queryKey: usersKey });
      onCreated(user);
    },
  });
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (create.isPending) return;
    const fields = new FormData(event.currentTarget);
    const username = String(fields.get('username'));
    const password = String(fields.get('password'));
    create.reset();
    if (!/^[A-Za-z0-9_.-]{3,64}$/.test(username)) {
      setValidation('Логин: от 3 до 64 символов. Допустимы латинские буквы, цифры, _, - и .');
      return;
    }
    if (Array.from(password).length < 12 || new TextEncoder().encode(password).length > 1024) {
      setValidation('Пароль: не менее 12 символов и не более 1024 байт UTF-8.');
      return;
    }
    setValidation('');
    create.mutate();
  }
  return (
    <section className={styles.panel} aria-labelledby="create-user-heading">
      <h2 id="create-user-heading"><UserPlus size={18} /> Новый пользователь</h2>
      <p className={styles.hint}>Учётная запись участника без доступа к документам. После создания выберите разрешённые документы ниже.</p>
      <form ref={form} onSubmit={submit} noValidate className={styles.form}>
        <div className={ui.field}>
          <label htmlFor="new-username">Логин</label>
          <input id="new-username" name="username" autoComplete="off" placeholder="Например, ivan.petrov" required minLength={3} maxLength={64} aria-describedby="username-help" disabled={create.isPending} />
          <span id="username-help" className={styles.hint}>3–64 символа: A–Z, a–z, 0–9, _, -, .</span>
        </div>
        <div className={ui.field}>
          <label htmlFor="new-password">Пароль</label>
          <input id="new-password" name="password" type="password" autoComplete="new-password" placeholder="Введите пароль" required aria-describedby="password-help" disabled={create.isPending} />
          <span id="password-help" className={styles.hint}>От 12 символов, до 1024 байт UTF-8. Сохраните пароль перед созданием.</span>
        </div>
        <div className={ui.field}>
          <label htmlFor="new-role">Роль</label>
          <select id="new-role" defaultValue="" disabled={create.isPending}>
            <option value="" disabled>Выберите роль</option>
            <option value="accountant">Бухгалтер</option>
            <option value="manager">Менеджер</option>
            <option value="lawyer">Юрист</option>
            <option value="hr">Кадровик</option>
            <option value="records">Делопроизводитель</option>
          </select>
        </div>
        <button type="submit" className={cx(ui.button, ui.primary)} disabled={create.isPending}>
          {create.isPending ? 'Создаём…' : 'Создать участника'}
        </button>
      </form>
      {validation && <ErrorNotice message={validation} />}
      {create.isError && <ErrorNotice message={`${creationError(create.error)} Для повторной отправки введите пароль заново.`} />}
      {create.isSuccess && <p role="status" className={styles.success}>Пользователь создан. Настройте доступ к документам ниже.</p>}
    </section>
  );
}

function Pagination({ offset, total, busy, onChange }: {
  offset: number; total: number; busy: boolean; onChange: (offset: number) => void;
}) {
  return (
    <div className={styles.pagination}>
      <span>{total ? `${offset + 1}–${Math.min(offset + 20, total)} из ${total}` : 'Всего: 0'}</span>
      <div>
        <button className={cx(ui.button, ui.secondary)} disabled={!offset || busy} onClick={() => onChange(Math.max(0, offset - 20))}>Назад</button>
        <button className={cx(ui.button, ui.secondary)} disabled={offset + 20 >= total || busy} onClick={() => onChange(offset + 20)}>Далее</button>
      </div>
    </div>
  );
}

function DocumentAccessRow({ document, user }: {
  document: { id: string; title: string }; user: ListedUser;
}) {
  const client = useQueryClient();
  const key = [...accessKey(document.id), user.id];
  const access = useQuery({ queryKey: key, queryFn: ({ signal }) => usersApi.hasAccess(document.id, user.id, signal) });
  const change = useMutation({
    mutationFn: async (grant: boolean) => {
      if (grant) await usersApi.grant(document.id, user.id);
      else await usersApi.revoke(document.id, user.id);
    },
    onSuccess: async () => {
      // Invalidate all users' views of this document; never assume a failed request succeeded.
      await client.invalidateQueries({ queryKey: accessKey(document.id) });
    },
  });
  return (
    <li className={styles.document}>
      <div className={styles['document-heading']}>
        <strong>{document.title}</strong>
        {access.isPending ? <Loading label="Проверяем доступ…" /> : access.isError ? null : (
          <button className={cx(ui.button, access.data ? styles['revoke-access'] : styles['grant-access'])}
            aria-label={`${access.data ? 'Отозвать' : 'Выдать'} доступ: ${document.title}`}
            disabled={change.isPending || access.isFetching || (!user.is_active && !access.data)}
            onClick={() => { change.reset(); change.mutate(!access.data); }}>
            {change.isPending ? 'Сохраняем…' : access.data ? 'Отозвать доступ' : 'Выдать доступ'}
          </button>
        )}
      </div>
      {!access.isPending && !access.isError && <span className={styles.hint}>{access.data ? 'Доступ разрешён' : 'Нет доступа'}</span>}
      {access.isError && <ErrorNotice error={access.error} onRetry={() => void access.refetch()} />}
      {change.isError && <ErrorNotice error={change.error} />}
    </li>
  );
}

function UserDocumentAccess({ user }: { user: ListedUser }) {
  const [offset, setOffset] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { heading.current?.focus(); }, []);
  const documents = useQuery({ queryKey: ['documents', 'user-access', offset], queryFn: ({ signal }) => documentsApi.list(offset, signal) });
  const client = useQueryClient();
  return (
    <section className={styles.panel} aria-labelledby="user-access-heading">
      <div className={styles['panel-heading']}>
        <h2 ref={heading} tabIndex={-1} id="user-access-heading">Доступ к документам: {user.username}</h2>
        <button className={cx(ui.button, ui.secondary)} onClick={() => {
          void documents.refetch();
          void client.invalidateQueries({ queryKey: ['document-access'] });
        }}>Обновить доступ</button>
      </div>
      <p className={styles.hint}>Разрешённые документы будут доступны участнику в базе знаний и помощнике. Новый пользователь начинает с пустой историей диалогов.</p>
      {!user.is_active && <p className={styles.hint}>Учётная запись неактивна. Можно отозвать ранее выданный доступ.</p>}
      {documents.isPending && <Loading label="Загружаем документы…" />}
      {documents.isError && <ErrorNotice error={documents.error} onRetry={() => void documents.refetch()} />}
      {!documents.isError && documents.data && <>
        {documents.data.items.length === 0 && <p className={styles.hint}>Документов пока нет. Добавьте их в базу знаний.</p>}
        <ul className={styles['document-list']}>
          {documents.data.items.map((document) => <DocumentAccessRow key={document.id} document={document} user={user} />)}
        </ul>
        <Pagination offset={offset} total={documents.data.total} busy={documents.isFetching} onChange={setOffset} />
      </>}
    </section>
  );
}

export function UsersPage() {
  const auth = useAuth();
  if (auth.isPending) return <Loading />;
  if (auth.isError) return <ErrorNotice error={auth.error} />;
  if (auth.data?.role !== 'admin') return <ErrorNotice message="Доступ запрещён. Раздел доступен только администратору." />;
  return <AdminUsersPage />;
}

function AdminUsersPage() {
  const client = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ListedUser | null>(null);
  const users = useQuery({ queryKey: [...usersKey, offset], queryFn: ({ signal }) => usersApi.list(offset, signal) });
  return (
    <section className={styles.page}>
      <div className={ui['page-heading']}>
        <div><span className={ui.eyebrow}>Администрирование</span><h1>Пользователи</h1><p>Учётные записи и доступ к документам организации.</p></div>
        <button className={cx(ui.button, ui.secondary)} disabled={users.isFetching} onClick={() => void users.refetch()}><RefreshCw size={16} />Обновить</button>
      </div>
      <CreateUser onCreated={(user) => {
        // The server lists oldest first; select the last page for the new account.
        const page = client.getQueryData<Collection<ListedUser>>([...usersKey, offset]);
        setOffset(Math.floor(Math.max(0, (page?.total ?? 1) - 1) / 20) * 20);
        setSelected(user);
      }} />
      <section className={styles.panel} aria-labelledby="users-heading">
        <h2 id="users-heading"><Users size={18} /> Учётные записи{users.data ? ` · ${users.data.total}` : ''}</h2>
        {users.isPending && <Loading label="Загружаем пользователей…" />}
        {users.isError && <ErrorNotice error={users.error} onRetry={() => void users.refetch()} />}
        {!users.isError && users.data && <>
          {users.data.items.length === 0 && <p className={styles.hint}>На этой странице нет пользователей.</p>}
          <div className={styles['table-scroll']}><table className={styles.table}>
            <thead><tr><th scope="col">Логин</th><th scope="col">Роль</th><th scope="col">Статус</th><th scope="col">Документы</th></tr></thead>
            <tbody>{users.data.items.map((user) => <tr key={user.id}>
              <td>{user.username}</td><td>{user.role === 'admin' ? 'Администратор' : 'Участник'}</td>
              <td><span className={user.is_active ? styles.active : styles.inactive}>{user.is_active ? 'Активен' : 'Неактивен'}</span></td>
              <td>{user.role === 'admin' ? <span className={styles.hint}>Все документы</span> : <button className={ui['text-button']} aria-pressed={selected?.id === user.id} onClick={() => setSelected(user)}>Настроить доступ: {user.username}</button>}</td>
            </tr>)}</tbody>
          </table></div>
          <Pagination offset={offset} total={users.data.total} busy={users.isFetching} onChange={setOffset} />
        </>}
      </section>
      {selected ? <UserDocumentAccess key={selected.id} user={selected} /> : <p className={styles.hint}>Выберите участника, чтобы настроить доступ к документам.</p>}
    </section>
  );
}
