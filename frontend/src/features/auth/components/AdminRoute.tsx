import { Outlet } from 'react-router-dom';
import { ErrorNotice, Loading } from '../../../shared/ui/Feedback';
import { useAuth } from '../hooks/useAuth';

export function AdminRoute() {
  const auth = useAuth();
  if (auth.isPending) return <Loading label="Проверяем права доступа…" />;
  if (auth.isError) return <ErrorNotice error={auth.error} />;
  if (auth.data?.role !== 'admin')
    return <ErrorNotice message="Доступ запрещён. Раздел доступен только администратору." />;
  return <Outlet />;
}
