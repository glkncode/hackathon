import { useMutation, useQuery } from '@tanstack/react-query';
import { templatesApi } from '../api/templates';

export function useTemplateResult(id: string) {
  const query = useQuery({
    queryKey: ['templates', 'result', id],
    queryFn: ({ signal }) => templatesApi.generated(id, signal),
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: 'always',
  });
  const result = query.data;
  const template = useQuery({
    queryKey: ['templates', 'detail', result?.template_id],
    queryFn: ({ signal }) => templatesApi.get(result!.template_id, signal),
    enabled: !!result,
    gcTime: 0,
  });
  const download = useMutation({ mutationFn: () => templatesApi.download(id, result!.filename) });
  return { query, result, template, download };
}
