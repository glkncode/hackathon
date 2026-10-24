import { useState, type FormEvent } from 'react';
import {
  ArrowRight,
  BookOpen,
  Check,
  Eye,
  EyeOff,
  FileText,
  LockKeyhole,
  Search,
  ShieldCheck,
} from 'lucide-react';
import { Navigate, useNavigate } from 'react-router-dom';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { authApi } from '../api/auth';
import { ApiError } from '../api/client';
import { authKey } from '../app/providers';
import { useAuth } from '../features/auth/useAuth';
import { ErrorNotice, Loading } from '../components/ui/Feedback';

export function LoginPage() {
  const auth = useAuth();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [visible, setVisible] = useState(false);
  const login = useMutation({
    mutationFn: async () => {
      await authApi.login(username.trim(), password);
      // The authoritative user is always loaded from /auth/me after cookies are set.
      const user = await authApi.me();
      if (!user) throw new ApiError(401);
      return user;
    },
    onSuccess: async (user) => {
      setPassword('');
      await client.cancelQueries();
      client.clear();
      client.setQueryData(authKey, user);
      navigate('/chat', { replace: true });
    },
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    if (username.trim() && password && !login.isPending) login.mutate();
  }
  if (auth.isPending)
    return (
      <div className="full-state">
        <Loading label="Проверяем сессию…" />
      </div>
    );
  if (auth.isSuccess && auth.data && !login.isPending) return <Navigate to="/chat" replace />;

  return (
    <div className="login-page">
      <section className="login-story" aria-label="О помощнике">
        <LinkBrand />
        <div className="login-story-main">
          <span className="eyebrow">
            <span className="small-dot" />
            ВАША ВНУТРЕННЯЯ БАЗА ЗНАНИЙ
          </span>
          <h1>
            Нужное положение.
            <br />
            <span>В нужный момент.</span>
          </h1>
          <p>
            Внутренние инструкции и регламенты —<br className="desktop-break" /> в одном понятном
            пространстве.
          </p>
          <div className="login-preview">
            <div className="preview-top">
              <span className="preview-icon">
                <Search size={19} />
              </span>
              <span>От вопроса — к источнику</span>
              <span className="preview-pill">Поиск</span>
            </div>
            <div className="preview-document">
              <FileText size={24} />
              <div>
                <strong>Ваши документы</strong>
                <span>Версии · Разделы · Точные фрагменты</span>
              </div>
              <span className="preview-check">
                <Check size={15} />
              </span>
            </div>
            <div className="preview-lines">
              <i />
              <i />
              <i />
            </div>
            <div className="preview-footer">
              <ShieldCheck size={14} />
              Доступ с учётом ваших прав
            </div>
          </div>
          <div className="login-benefits">
            <span>
              <Search size={17} />
              Поиск по содержимому
            </span>
            <span>
              <BookOpen size={17} />
              Проверяемые источники
            </span>
          </div>
        </div>
        <div className="login-story-footer">
          AI Document Assistant <span>Работайте с информацией уверенно</span>
        </div>
      </section>
      <section className="login-form-side">
        <div className="login-form-box">
          <span className="login-lock">
            <LockKeyhole size={24} />
          </span>
          <span className="eyebrow form-eyebrow">ДОБРО ПОЖАЛОВАТЬ</span>
          <h2>
            Войдите в рабочее
            <br />
            пространство
          </h2>
          <p className="login-subtitle">Используйте учётную запись вашей организации.</p>
          {auth.isError && <ErrorNotice error={auth.error} onRetry={() => void auth.refetch()} />}
          <form onSubmit={submit} className="login-form">
            <div className="field">
              <label htmlFor="username">Имя пользователя</label>
              <input
                id="username"
                name="username"
                autoComplete="username"
                placeholder="Введите имя пользователя"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                required
                disabled={login.isPending}
                autoFocus
              />
            </div>
            <div className="field">
              <label htmlFor="password">Пароль</label>
              <div className="password-field">
                <input
                  id="password"
                  name="password"
                  type={visible ? 'text' : 'password'}
                  autoComplete="current-password"
                  placeholder="Введите пароль"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  required
                  disabled={login.isPending}
                />
                <button
                  className="icon-button"
                  type="button"
                  onClick={() => setVisible(!visible)}
                  aria-label={visible ? 'Скрыть пароль' : 'Показать пароль'}
                  aria-pressed={visible}
                >
                  {visible ? <EyeOff size={18} /> : <Eye size={18} />}
                </button>
              </div>
            </div>
            {login.isError && (
              <ErrorNotice
                error={login.error}
                message={
                  login.error instanceof ApiError && login.error.status === 401
                    ? 'Неверное имя пользователя или пароль либо сессия не установлена.'
                    : undefined
                }
              />
            )}
            <button
              type="submit"
              className="button primary login-submit"
              disabled={login.isPending || !username.trim() || !password}
            >
              {login.isPending ? (
                <Loading label="Входим…" />
              ) : (
                <>
                  Войти
                  <ArrowRight size={18} />
                </>
              )}
            </button>
          </form>
          <div className="login-security">
            <ShieldCheck size={16} />
            <span>Защищённый доступ к документам организации</span>
          </div>
          <p className="login-help">Нет учётной записи? Обратитесь к администратору.</p>
        </div>
        <footer className="login-form-footer">Внутренний помощник по документам</footer>
      </section>
    </div>
  );
}

function LinkBrand() {
  return (
    <div className="brand login-brand">
      <span className="brand-icon">
        <FileText size={23} />
      </span>
      <span>
        AI Document<span className="brand-subtitle">ASSISTANT</span>
      </span>
    </div>
  );
}
