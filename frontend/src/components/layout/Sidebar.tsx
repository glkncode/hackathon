import { BookOpen, FileText, LogOut, MessageSquare, Plus, ShieldCheck, X } from 'lucide-react';
import { Link, NavLink, useSearchParams } from 'react-router-dom';
import { useAuth, useLogout } from '../../features/auth/useAuth';
import { useChatSessions } from '../../hooks/useChatSessions';
import { ErrorNotice, Loading } from '../ui/Feedback';

export function Sidebar({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data: user } = useAuth();
  const logout = useLogout();
  const history = useChatSessions();
  const [params] = useSearchParams();
  const sessions = history.data?.pages.flatMap((page) => page.sessions) ?? [];
  const available = history.data?.pages[0]?.available;

  return (
    <>
      {open && <button className="sidebar-backdrop" aria-label="Закрыть меню" onClick={onClose} />}
      <aside
        className={`sidebar ${open ? 'is-open' : ''}`}
        aria-label="Основная навигация"
        id="main-sidebar"
      >
        <Link to="/chat" className="brand" onClick={onClose}>
          <span className="brand-icon">
            <FileText size={23} />
          </span>
          <span>
            AI Document<span className="brand-subtitle">ASSISTANT</span>
          </span>
        </Link>
        <button className="icon-button mobile-close" aria-label="Закрыть меню" onClick={onClose}>
          <X size={20} />
        </button>
        <Link className="button primary new-chat" to="/chat" onClick={onClose}>
          <Plus size={18} />
          Новый чат
        </Link>
        <nav className="primary-nav">
          <NavLink
            to="/chat"
            className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
            onClick={onClose}
          >
            <MessageSquare size={18} />
            Помощник
          </NavLink>
          <NavLink
            to="/documents"
            className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}
            onClick={onClose}
          >
            <BookOpen size={18} />
            База знаний
          </NavLink>
        </nav>
        <div className="history-heading">СОХРАНЁННЫЕ ДИАЛОГИ</div>
        <div className="history-list">
          {history.isPending && <Loading label="Загружаем историю…" />}
          {history.isError && (
            <ErrorNotice error={history.error} onRetry={() => void history.refetch()} />
          )}
          {available === false && (
            <div className="history-empty">
              <MessageSquare size={22} />
              <p>История пока недоступна</p>
              <span>Поиск работает без сохранения диалогов.</span>
              <button className="text-button" onClick={() => void history.refetch()}>
                Проверить снова
              </button>
            </div>
          )}
          {available && sessions.length === 0 && (
            <div className="history-empty">
              <MessageSquare size={22} />
              <p>Здесь будут ваши диалоги</p>
              <span>Задайте первый вопрос помощнику.</span>
            </div>
          )}
          {sessions.map((session) => (
            <Link
              key={session.id}
              to={`/chat?session=${encodeURIComponent(session.id)}`}
              className={`history-link ${params.get('session') === session.id ? 'selected' : ''}`}
              onClick={onClose}
            >
              <MessageSquare size={15} />
              <span>{session.title}</span>
            </Link>
          ))}
          {history.hasNextPage && (
            <button
              className="text-button history-more"
              disabled={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              {history.isFetchingNextPage ? 'Загрузка…' : 'Показать ещё'}
            </button>
          )}
        </div>
        <div className="sidebar-note">
          <ShieldCheck size={17} />
          <span>
            Только доступные вам
            <br />
            документы и источники
          </span>
        </div>
        <div className="user-area">
          <span className="avatar">{user?.username.slice(0, 2).toUpperCase()}</span>
          <div className="user-info">
            <strong>{user?.username}</strong>
            <span>{user?.role === 'admin' ? 'Администратор' : 'Участник'}</span>
          </div>
          <button
            className="icon-button"
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
