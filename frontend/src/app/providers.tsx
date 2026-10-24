import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useState, type ReactNode } from 'react';
import { clearSession } from '../features/auth';
import { SESSION_EXPIRED_EVENT } from '../shared/api/client';

export function Providers({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false, staleTime: 0, gcTime: 0, refetchOnWindowFocus: true },
          mutations: { retry: false, gcTime: 0 },
        },
      }),
  );
  useEffect(() => {
    const expire = () => {
      void clearSession(client);
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, expire);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, expire);
  }, [client]);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
