import { queryOptions, useInfiniteQuery } from '@tanstack/react-query';
import { ApiError } from '../../../shared/api/client';
import { templatesApi } from '../api/templates';

// V2 includes validation for this exact revision. Never overwrite it with a
// second, unversioned next-question response from a potentially newer draft.
export async function loadTemplateDraft(id: string, signal?: AbortSignal) {
  let draft = await templatesApi.draft(id, signal);
  if (draft.validation) return draft;
  // Legacy drafts expose validation separately. Check that no writer changed
  // the revision while the report was being read.
  for (let attempt = 0; attempt < 3; attempt++) {
    const validation = await templatesApi.next(id, signal);
    const latest = await templatesApi.draft(id, signal);
    if (latest.validation) return latest;
    if (latest.revision === draft.revision) return { ...latest, validation };
    draft = latest;
  }
  throw new ApiError(409);
}

export const templateKeys = {
  root: ['templates'] as const,
  draft: (id: string) => ['templates', 'draft', id] as const,
};
export const draftOptions = (id: string) =>
  queryOptions({
    queryKey: templateKeys.draft(id),
    queryFn: ({ signal }) => loadTemplateDraft(id, signal),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchOnMount: 'always',
    // An automatic focus refresh must not replace an in-progress question and
    // destroy unsaved input. Writes still use CAS; conflicts refresh explicitly.
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
export function useTemplateCatalog() {
  return useInfiniteQuery({
    queryKey: ['templates', 'catalog'],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => templatesApi.list(pageParam, signal),
    getNextPageParam: (page) =>
      (page.items?.length ?? 0) === page.limit ? page.offset + page.limit : undefined,
    staleTime: 0,
    gcTime: 0,
  });
}
export function useTemplateDrafts() {
  return useInfiniteQuery({
    queryKey: ['templates', 'drafts'],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => templatesApi.drafts(pageParam, signal),
    getNextPageParam: (page) =>
      (page.items?.length ?? 0) === page.limit ? page.offset + page.limit : undefined,
    staleTime: 0,
    gcTime: 0,
  });
}
