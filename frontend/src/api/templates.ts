import { apiFetch, apiRequest, ApiError } from './client';
import { saveDownload } from '../lib/download';
import type { DocumentTemplate, TemplateDraft, TemplateValidation, TemplatePage, FieldValue, GenerationResult, GeneratedDocument, TemplateProfile } from '../types/templates';

const draftPath = (id: string) => `/template-drafts/${encodeURIComponent(id)}`;
export const DOCX_MIME = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
export const templatesApi = {
  list: (offset = 0, signal?: AbortSignal) => apiRequest<TemplatePage<DocumentTemplate>>(`/templates?limit=20&offset=${offset}`, { signal }),
  drafts: (offset = 0, signal?: AbortSignal) => apiRequest<TemplatePage<TemplateDraft>>(`/template-drafts?limit=20&offset=${offset}`, { signal }),
  get: (id: string, signal?: AbortSignal) => apiRequest<DocumentTemplate>(`/templates/${encodeURIComponent(id)}`, { signal }),
  create: (id: string) => apiRequest<TemplateDraft>(`/templates/${encodeURIComponent(id)}/drafts`, { method: 'POST' }),
  draft: (id: string, signal?: AbortSignal) => apiRequest<TemplateDraft>(draftPath(id), { signal }),
  next: (id: string, signal?: AbortSignal) => apiRequest<TemplateValidation>(`${draftPath(id)}/next-question`, { signal }),
  answer: (id: string, revision: number, field_key: string, value: FieldValue) => apiRequest<TemplateDraft>(`${draftPath(id)}/answers`, { method: 'POST', body: { expected_revision: revision, field_key, value } }),
  update: (id: string, revision: number, values: Record<string, FieldValue>) => apiRequest<TemplateDraft>(draftPath(id), { method: 'PATCH', body: { expected_revision: revision, values } }),
  validate: (id: string) => apiRequest<TemplateValidation>(`${draftPath(id)}/validate`, { method: 'POST' }),
  generate: (id: string, revision: number) => apiRequest<GenerationResult>(`${draftPath(id)}/generate`, { method: 'POST', body: { expected_revision: revision } }),
  generated: (id: string, signal?: AbortSignal) => apiRequest<GeneratedDocument>(`/generated-documents/${encodeURIComponent(id)}`, { signal }),
  register: (input: { document_id: string; document_version_id: string; name: string; description: string; profile: TemplateProfile }) => apiRequest<DocumentTemplate>('/templates', { method: 'POST', body: input }),
  download: async (id: string, filename: string) => {
    const response = await apiFetch(`/generated-documents/${encodeURIComponent(id)}/download`);
    if (response.status !== 200 || response.headers.get('Content-Type')?.split(';')[0] !== DOCX_MIME || !response.headers.get('Content-Disposition')) throw new ApiError(502);
    await saveDownload(response, filename);
  },
};

// Only a structured validation report is eligible for display; server error strings stay private.
export function validationFromError(error: unknown): TemplateValidation | undefined {
  if (!(error instanceof ApiError) || error.status !== 422 || !error.details || typeof error.details !== 'object') return;
  const data = error.details as Partial<TemplateValidation>;
  if (typeof data.ready === 'boolean' && Array.isArray(data.missing_fields) && Array.isArray(data.invalid_fields)) return data as TemplateValidation;
}
