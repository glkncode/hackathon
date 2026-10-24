import { useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import { authKey } from '../model/session';
import type { User } from '../types/auth';

// Read at mutation completion, not from a stale render before logout.
export function useCurrentUserGuard() {
  const client = useQueryClient();
  return useCallback(
    (ownerId: string) => client.getQueryData<User | null>(authKey)?.id === ownerId,
    [client],
  );
}
