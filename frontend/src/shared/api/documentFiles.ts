import { apiFetch } from './client';

export interface DocumentVersionReference {
  document_id: string;
  document_version_id: string;
}

// Shared file transport used by document versions and cited chat sources.
export function downloadDocumentVersion(source: DocumentVersionReference, signal?: AbortSignal) {
  return apiFetch(
    `/documents/${encodeURIComponent(source.document_id)}/versions/${encodeURIComponent(source.document_version_id)}/download`,
    { signal },
  );
}
