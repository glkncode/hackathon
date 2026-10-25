import { Menu } from 'lucide-react';
import { cx } from '../../shared/lib/cx';
import ui from '../../shared/ui/ui.module.css';
import styles from './Header.module.css';

export function Header({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  return (
    <header className={styles['header']}>
      <button
        className={cx(ui['icon-button'], styles['menu-toggle'])}
        aria-label="Открыть меню"
        aria-expanded={menuOpen}
        aria-controls="main-sidebar"
        onClick={onOpenMenu}
      >
        <Menu size={21} />
      </button>
    </header>
  );
}
