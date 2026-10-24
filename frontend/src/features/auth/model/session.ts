import { QueryClient } from '@tanstack/react-query';

export const authKey = ['auth', 'me'] as const;

export async function clearSession(client: QueryClient) {
  await client.cancelQueries();
  // Preserve the observed auth query so active routes receive the null update.
  client.setQueryData(authKey, null);
  client.removeQueries({ predicate: (query) => query.queryKey[0] !== 'auth' });
  client.getMutationCache().clear();
}
