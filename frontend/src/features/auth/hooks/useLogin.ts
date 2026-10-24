import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from '../../../shared/api/client';
import { authApi } from '../api/auth';
import { authKey } from '../model/session';
import { useAuth } from './useAuth';

export function useLogin() {
  const auth = useAuth();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
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
  return { auth, username, setUsername, password, setPassword, login, submit };
}
