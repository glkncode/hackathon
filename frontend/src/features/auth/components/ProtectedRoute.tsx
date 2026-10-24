import { Navigate, Outlet } from 'react-router-dom';
import { Loading } from '../../../shared/ui/Feedback';
import ui from '../../../shared/ui/ui.module.css';
import { useAuth } from '../hooks/useAuth';

export function ProtectedRoute() {
  const auth = useAuth();
  if (auth.isPending)
    return (
      <div className={ui['full-state']}>
        <Loading label="Проверяем сессию…" />
      </div>
    );
  // Login keeps the session-check failure visible and offers an explicit retry.
  if (auth.isError) return <Navigate to="/login" replace />;
  if (!auth.data) return <Navigate to="/login" replace />;
  return <Outlet />;
}
