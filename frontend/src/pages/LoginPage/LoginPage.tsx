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
import { useState } from 'react';
import { Navigate } from 'react-router-dom';
import { useLogin } from '../../features/auth';
import { ApiError } from '../../shared/api/client';
import { cx } from '../../shared/lib/cx';
import { ErrorNotice, Loading } from '../../shared/ui/Feedback';
import ui from '../../shared/ui/ui.module.css';
import styles from './LoginPage.module.css';

export function LoginPage() {
  const { auth, username, setUsername, password, setPassword, login, submit } = useLogin();
  const [visible, setVisible] = useState(false);
  if (auth.isPending)
    return (
      <div className={ui['full-state']}>
        <Loading label="Проверяем сессию…" />
      </div>
    );
  if (auth.isSuccess && auth.data && !login.isPending) return <Navigate to="/chat" replace />;

  return (
    <div className={styles['login-page']}>
      <section className={styles['login-story']} aria-label="О помощнике">
        <LinkBrand />
        <div className={styles['login-story-main']}>
          <span className={ui['eyebrow']}>
            <span className={styles['small-dot']} />
            Внутренняя база знаний
          </span>
          <h1>
            Нужное положение.
            <br />
            <span>В нужный момент.</span>
          </h1>
          <p>
            Внутренние инструкции и регламенты —<br /> в одном понятном пространстве.
          </p>
          <div className={styles['login-preview']}>
            <div className={styles['preview-top']}>
              <span className={styles['preview-icon']}>
                <Search size={19} />
              </span>
              <span>От вопроса — к источнику</span>
              <span className={styles['preview-pill']}>Поиск</span>
            </div>
            <div className={styles['preview-document']}>
              <FileText size={24} />
              <div>
                <strong>Ваши документы</strong>
                <span>Версии · Разделы · Точные фрагменты</span>
              </div>
              <span className={styles['preview-check']}>
                <Check size={15} />
              </span>
            </div>
            <div className={styles['preview-lines']}>
              <i />
              <i />
              <i />
            </div>
            <div className={styles['preview-footer']}>
              <ShieldCheck size={14} />
              Доступ с учётом ваших прав
            </div>
          </div>
          <div className={styles['login-benefits']}>
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
        <div className={styles['login-story-footer']}>
          Помощник по документам
        </div>
      </section>
      <section className={styles['login-form-side']}>
        <div className={styles['login-form-box']}>
          <span className={styles['login-lock']}>
            <LockKeyhole size={24} />
          </span>
          <span className={cx(ui['eyebrow'], styles['form-eyebrow'])}>Добро пожаловать</span>
          <h2>
            Войдите в рабочее
            <br />
            пространство
          </h2>
          <p className={styles['login-subtitle']}>Используйте учётную запись вашей организации.</p>
          {auth.isError && <ErrorNotice error={auth.error} onRetry={() => void auth.refetch()} />}
          <form onSubmit={submit} className={styles['login-form']}>
            <div className={ui['field']}>
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
            <div className={ui['field']}>
              <label htmlFor="password">Пароль</label>
              <div className={styles['password-field']}>
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
                  className={ui['icon-button']}
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
              className={cx(ui['button'], ui['primary'], styles['login-submit'])}
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
          <div className={styles['login-security']}>
            <ShieldCheck size={16} />
            <span>Защищённый доступ к документам организации</span>
          </div>
          <p className={styles['login-help']}>Нет учётной записи? Обратитесь к администратору.</p>
        </div>
        <footer className={styles['login-form-footer']}>Внутренний помощник по документам</footer>
      </section>
    </div>
  );
}

function LinkBrand() {
  return (
    <div className={cx(ui['brand'], styles['login-brand'])}>
      <span className={ui['brand-icon']}>
        <FileText size={23} />
      </span>
      <span>
        Помощник<span className={ui['brand-subtitle']}>по документам</span>
      </span>
    </div>
  );
}
