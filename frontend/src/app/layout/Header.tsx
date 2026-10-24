import { BookOpen, FileText, Menu, MessageSquare } from 'lucide-react';
import { useLocation } from 'react-router-dom';
import { cx } from '../../shared/lib/cx';
import ui from '../../shared/ui/ui.module.css';
import styles from './Header.module.css';

export function Header({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  const pathname = useLocation().pathname;
  const documents = pathname === '/documents';
  const templates = pathname.startsWith('/templates');
  return (
    <header className={styles['header']}>
      <div className={styles['header-title']}>
        <button
          className={cx(ui['icon-button'], styles['menu-toggle'])}
          aria-label="Открыть меню"
          aria-expanded={menuOpen}
          aria-controls="main-sidebar"
          onClick={onOpenMenu}
        >
          <Menu size={21} />
        </button>
        {templates ? (
          <FileText size={18} />
        ) : documents ? (
          <BookOpen size={18} />
        ) : (
          <MessageSquare size={18} />
        )}
        <span>
          {templates
            ? 'Подготовка документов'
            : documents
              ? 'База знаний'
              : 'Помощник по документам'}
        </span>
      </div>
      <span className={styles['mode-badge']}>
        <BookOpen size={13} />
        <span>Документы организации</span>
      </span>
    </header>
  );
}
