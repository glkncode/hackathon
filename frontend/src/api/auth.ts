import { ApiError, apiRequest } from './client';
import type { User } from '../types/api';

export const authApi = {
  async me(signal?: AbortSignal): Promise<User | null> {
    try {
      return await apiRequest<User>('/auth/me', { signal });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return null;
      throw error;
    }
  },
  login: (username: string, password: string) =>
    apiRequest<{ user: User }>('/auth/login', {
      method: 'POST',
      body: { username, password },
    }),
  logout: () => apiRequest('/auth/logout', { method: 'POST' }),
};
