import { BookOpen, Menu, MessageSquare } from 'lucide-react';
import { useLocation } from 'react-router-dom';

export function Header({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  const documents = useLocation().pathname === '/documents';
  return (
    <header className="header">
      <div className="header-title">
        <button
          className="icon-button menu-toggle"
          aria-label="Открыть меню"
          aria-expanded={menuOpen}
          aria-controls="main-sidebar"
          onClick={onOpenMenu}
        >
          <Menu size={21} />
        </button>
        {documents ? <BookOpen size={18} /> : <MessageSquare size={18} />}
        <span>{documents ? 'База знаний' : 'Помощник по документам'}</span>
      </div>
      <span className="mode-badge">
        <BookOpen size={13} />
        <span>Документы организации</span>
      </span>
    </header>
  );
}
