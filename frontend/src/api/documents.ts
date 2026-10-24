import { apiFetch, apiRequest } from './client';
import type {
  Collection,
  DocumentDetails,
  DocumentRecord,
  DocumentVersion,
  Source,
} from '../types/api';

export const documentsApi = {
  list: (offset = 0, signal?: AbortSignal) =>
    apiRequest<Collection<DocumentRecord>>(`/documents?limit=20&offset=${offset}`, { signal }),
  get: (id: string, signal?: AbortSignal) =>
    apiRequest<DocumentDetails>(`/documents/${encodeURIComponent(id)}`, { signal }),
  versions: (id: string, signal?: AbortSignal) =>
    apiRequest<Collection<DocumentVersion>>(
      `/documents/${encodeURIComponent(id)}/versions?limit=1&offset=0`,
      { signal },
    ),
  upload: (title: string, file: File) => {
    const body = new FormData();
    body.append('title', title);
    body.append('file', file);
    return apiRequest<DocumentDetails>('/documents', { method: 'POST', body });
  },
  download: (id: string, signal?: AbortSignal) =>
    apiFetch(`/documents/${encodeURIComponent(id)}/download`, { signal }),
  downloadSource: (source: Source, signal?: AbortSignal) =>
    apiFetch(
      `/documents/${encodeURIComponent(source.document_id)}/versions/${encodeURIComponent(source.document_version_id)}/download`,
      { signal },
    ),
};
