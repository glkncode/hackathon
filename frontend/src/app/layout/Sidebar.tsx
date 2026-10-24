import { BookOpen, FileText, LogOut, MessageSquare, Plus, ShieldCheck, X } from 'lucide-react';
import { Link, NavLink, useSearchParams } from 'react-router-dom';
import { useAuth, useLogout } from '../../features/auth';
import { useChatSessions } from '../../features/chat';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './Sidebar.module.css';

export function Sidebar({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data: user } = useAuth();
  const logout = useLogout();
  const history = useChatSessions();
  const [params] = useSearchParams();
  const sessions = history.data?.pages.flatMap((page) => page.sessions) ?? [];
  const available = history.data?.pages[0]?.available;

  return (
    <>
      {open && (
        <button
          className={styles['sidebar-backdrop']}
          aria-label="Закрыть меню"
          onClick={onClose}
        />
      )}
      <aside
        className={cx(styles['sidebar'], open ? styles['is-open'] : '')}
        aria-label="Основная навигация"
        id="main-sidebar"
      >
        <Link to="/chat" className={ui['brand']} onClick={onClose}>
          <span className={ui['brand-icon']}>
            <FileText size={23} />
          </span>
          <span>
            AI Document<span className={ui['brand-subtitle']}>ASSISTANT</span>
          </span>
        </Link>
        <button
          className={cx(ui['icon-button'], styles['mobile-close'])}
          aria-label="Закрыть меню"
          onClick={onClose}
        >
          <X size={20} />
        </button>
        <Link
          className={cx(ui['button'], ui['primary'], styles['new-chat'])}
          to="/chat"
          onClick={onClose}
        >
          <Plus size={18} />
          Новый чат
        </Link>
        <nav className={styles['primary-nav']}>
          <NavLink
            to="/templates"
            className={({ isActive }) => cx(styles['nav-link'], isActive ? styles['active'] : '')}
            onClick={onClose}
          >
            <FileText size={18} />
            Шаблоны
          </NavLink>
          <NavLink
            to="/chat"
            className={({ isActive }) => cx(styles['nav-link'], isActive ? styles['active'] : '')}
            onClick={onClose}
          >
            <MessageSquare size={18} />
            Помощник
          </NavLink>
          <NavLink
            to="/documents"
            className={({ isActive }) => cx(styles['nav-link'], isActive ? styles['active'] : '')}
            onClick={onClose}
          >
            <BookOpen size={18} />
            База знаний
          </NavLink>
        </nav>
        <div className={styles['history-heading']}>СОХРАНЁННЫЕ ДИАЛОГИ</div>
        <div className={styles['history-list']}>
          {history.isPending && <Loading label="Загружаем историю…" />}
          {history.isError && (
            <ErrorNotice error={history.error} onRetry={() => void history.refetch()} />
          )}
          {available === false && (
            <div className={styles['history-empty']}>
              <MessageSquare size={22} />
              <p>История пока недоступна</p>
              <span>Поиск работает без сохранения диалогов.</span>
              <button className={ui['text-button']} onClick={() => void history.refetch()}>
                Проверить снова
              </button>
            </div>
          )}
          {available && sessions.length === 0 && (
            <div className={styles['history-empty']}>
              <MessageSquare size={22} />
              <p>Здесь будут ваши диалоги</p>
              <span>Задайте первый вопрос помощнику.</span>
            </div>
          )}
          {sessions.map((session) => (
            <Link
              key={session.id}
              to={`/chat?session=${encodeURIComponent(session.id)}`}
              className={cx(
                styles['history-link'],
                params.get('session') === session.id ? styles['selected'] : '',
              )}
              onClick={onClose}
            >
              <MessageSquare size={15} />
              <span>{session.title}</span>
            </Link>
          ))}
          {history.hasNextPage && (
            <button
              className={cx(ui['text-button'], styles['history-more'])}
              disabled={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              {history.isFetchingNextPage ? 'Загрузка…' : 'Показать ещё'}
            </button>
          )}
        </div>
        <div className={styles['sidebar-note']}>
          <ShieldCheck size={17} />
          <span>
            Только доступные вам
            <br />
            документы и источники
          </span>
        </div>
        <div className={styles['user-area']}>
          <span className={styles['avatar']}>{user?.username.slice(0, 2).toUpperCase()}</span>
          <div className={styles['user-info']}>
            <strong>{user?.username}</strong>
            <span>{user?.role === 'admin' ? 'Администратор' : 'Участник'}</span>
          </div>
          <button
            className={ui['icon-button']}
            aria-label="Выйти из аккаунта"
            disabled={logout.isPending}
            onClick={() => logout.mutate()}
          >
            <LogOut size={18} />
          </button>
        </div>
        {logout.isError && <ErrorNotice error={logout.error} />}
      </aside>
    </>
  );
}
