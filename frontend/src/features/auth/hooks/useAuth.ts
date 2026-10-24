import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { authApi } from '../api/auth';
import { authKey, clearSession } from '../model/session';

export function useAuth() {
  return useQuery({
    queryKey: authKey,
    queryFn: ({ signal }) => authApi.me(signal),
    gcTime: 5 * 60_000,
    staleTime: 30_000,
  });
}

export function useLogout() {
  const client = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: authApi.logout,
    onSuccess: async () => {
      await clearSession(client);
      navigate('/login', { replace: true });
    },
  });
}
