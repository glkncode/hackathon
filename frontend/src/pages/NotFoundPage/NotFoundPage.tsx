import { ArrowLeft, FileQuestion } from 'lucide-react';
import { Link } from 'react-router-dom';
import { cx } from '../../shared/lib/cx';
import ui from '../../shared/ui/ui.module.css';
import styles from './NotFoundPage.module.css';

export function NotFoundPage() {
  return (
    <div className={styles['not-found']}>
      <FileQuestion size={42} />
      <span className={ui['eyebrow']}>ОШИБКА 404</span>
      <h1>Страница не найдена</h1>
      <p>Возможно, ссылка устарела или в адресе есть опечатка.</p>
      <Link to="/chat" className={cx(ui['button'], ui['primary'])}>
        <ArrowLeft size={17} />К помощнику
      </Link>
    </div>
  );
}
