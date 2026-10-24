import { ArrowLeft, FileQuestion } from 'lucide-react';
import { Link } from 'react-router-dom';

export function NotFoundPage() {
  return (
    <div className="not-found">
      <FileQuestion size={42} />
      <span className="eyebrow">ОШИБКА 404</span>
      <h1>Страница не найдена</h1>
      <p>Возможно, ссылка устарела или в адресе есть опечатка.</p>
      <Link to="/chat" className="button primary">
        <ArrowLeft size={17} />К помощнику
      </Link>
    </div>
  );
}
