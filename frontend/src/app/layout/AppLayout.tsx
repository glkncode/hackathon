import { useEffect, useRef, useState } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import styles from './AppLayout.module.css';
import { Header } from './Header';
import { Sidebar } from './Sidebar';

export function AppLayout() {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const closeOnDesktop = () => {
      if (window.innerWidth > 900) setOpen(false);
    };
    window.addEventListener('resize', closeOnDesktop);
    return () => window.removeEventListener('resize', closeOnDesktop);
  }, []);

  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement;
    const sidebar = menuRef.current?.querySelector<HTMLElement>('#main-sidebar');
    sidebar?.querySelector<HTMLElement>('a, button')?.focus();
    const close = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
      if (event.key !== 'Tab' || !sidebar) return;
      const elements = [
        ...sidebar.querySelectorAll<HTMLElement>('a, button:not(:disabled)'),
      ].filter((element) => element.getClientRects().length > 0);
      const first = elements[0];
      const last = elements.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener('keydown', close);
    return () => {
      document.removeEventListener('keydown', close);
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, [open]);

  return (
    <div className={styles['app-layout']}>
      <a className={styles['skip-link']} href="#main-content">
        К содержимому
      </a>
      <div ref={menuRef}>
        <Sidebar open={open} onClose={() => setOpen(false)} />
      </div>
      <div className={styles['main-shell']} inert={open || undefined}>
        <Header onOpenMenu={() => setOpen(true)} menuOpen={open} />
        <main id="main-content" className={styles['main-content']}>
          <Outlet key={location.pathname === '/chat' ? location.key : location.pathname} />
        </main>
      </div>
    </div>
  );
}
