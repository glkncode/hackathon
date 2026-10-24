import { BookOpen, FileText, Menu, MessageSquare } from 'lucide-react';
import { useLocation } from 'react-router-dom';

export function Header({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  const pathname = useLocation().pathname;
  const documents = pathname === '/documents';
  const templates = pathname.startsWith('/templates');
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
        {templates ? <FileText size={18} /> : documents ? <BookOpen size={18} /> : <MessageSquare size={18} />}
        <span>{templates ? 'Подготовка документов' : documents ? 'База знаний' : 'Помощник по документам'}</span>
      </div>
      <span className="mode-badge">
        <BookOpen size={13} />
        <span>Документы организации</span>
      </span>
    </header>
  );
}
