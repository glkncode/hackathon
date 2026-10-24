import { apiRequest } from './client';
import type { SearchResponse } from '../types/api';

export const searchApi = {
  search: (query: string, signal?: AbortSignal) =>
    apiRequest<SearchResponse>('/search', {
      method: 'POST',
      body: { query, limit: 10 },
      signal,
    }),
};
