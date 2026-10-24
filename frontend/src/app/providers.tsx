import { useEffect, useState, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SESSION_EXPIRED_EVENT } from '../api/client';

export const authKey = ['auth', 'me'] as const;

export async function clearSession(client: QueryClient) {
  await client.cancelQueries();
  // Preserve the observed auth query so active routes receive the null update.
  client.setQueryData(authKey, null);
  client.removeQueries({ predicate: (query) => query.queryKey[0] !== 'auth' });
  client.getMutationCache().clear();
}

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
