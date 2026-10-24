import { Navigate, Outlet } from 'react-router-dom';
import { useAuth } from './useAuth';
import { Loading } from '../../components/ui/Feedback';

export function ProtectedRoute() {
  const auth = useAuth();
  if (auth.isPending)
    return (
      <div className="full-state">
        <Loading label="Проверяем сессию…" />
      </div>
    );
  // Login keeps the session-check failure visible and offers an explicit retry.
  if (auth.isError) return <Navigate to="/login" replace />;
  if (!auth.data) return <Navigate to="/login" replace />;
  return <Outlet />;
}
