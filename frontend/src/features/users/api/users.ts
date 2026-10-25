import { apiRequest } from '../../../shared/api/client';
import type { Collection } from '../../../shared/types/collection';

// internal/transport/http/handlers/common.go: listedUser (also used by ACL lists).
export interface ListedUser {
  id: string;
  username: string;
  role: 'admin' | 'member';
  is_active: boolean;
}

interface DocumentAccess {
  id: string;
  document_id: string;
  user_id: string;
  granted_by: string;
  created_at: string;
}

const accessPath = (id: string) => `/documents/${encodeURIComponent(id)}/access`;

export const usersKey = ['users'] as const;
export const accessKey = (id: string) => ['document-access', id] as const;

export const usersApi = {
  list: (offset = 0, signal?: AbortSignal) =>
    apiRequest<Collection<ListedUser>>(`/users?limit=20&offset=${offset}`, { signal }),
  // The server always creates an active member. Never send a client-selected role.
  create: (username: string, password: string) =>
    apiRequest<ListedUser>('/users', { method: 'POST', body: { username, password } }),
  async hasAccess(documentId: string, userId: string, signal?: AbortSignal): Promise<boolean> {
    // There is no per-user ACL endpoint. Check every page before declaring no access.
    for (let offset = 0; ; offset += 100) {
      const page = await apiRequest<Collection<ListedUser>>(
        `${accessPath(documentId)}?limit=100&offset=${offset}`, { signal },
      );
      if (page.items.some((user) => user.id === userId)) return true;
      if (offset + page.items.length >= page.total) return false;
      if (page.items.length === 0) throw new Error('Incomplete ACL response');
    }
  },
  grant: (documentId: string, userId: string) =>
    apiRequest<DocumentAccess>(accessPath(documentId), {
      method: 'POST', body: { user_id: userId },
    }),
  revoke: (documentId: string, userId: string) =>
    apiRequest(`${accessPath(documentId)}/${encodeURIComponent(userId)}`, { method: 'DELETE' }),
};
