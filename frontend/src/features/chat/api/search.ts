import { apiRequest } from '../../../shared/api/client';
import type { SearchResponse } from '../types/chat';

export const searchApi = {
  search: (query: string, signal?: AbortSignal) =>
    apiRequest<SearchResponse>('/search', {
      method: 'POST',
      body: { query, limit: 10 },
      signal,
    }),
};
