import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from '../../../shared/api/client';
import { templatesApi, validationFromError } from '../api/templates';
import type { TemplateDraft, TemplateValidation } from '../types/templates';
import { draftOptions, templateKeys } from './useTemplates';

export function useTemplateDraftController(
  id: string,
  isCurrentOwner: (ownerId: string) => boolean,
) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const query = useQuery(draftOptions(id));
  const draft = query.data;
  const template = useQuery({
    queryKey: ['templates', 'detail', draft?.template_id],
    queryFn: ({ signal }) => templatesApi.get(draft!.template_id, signal),
    enabled: !!draft,
    gcTime: 0,
  });
  const [review, setReview] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [saved, setSaved] = useState('');
  const generateLock = useRef(false);
  const applyReport = (report: TemplateValidation) => {
    setReview(true);
    client.setQueryData<TemplateDraft>(templateKeys.draft(id), (previous) =>
      previous ? { ...previous, validation: report } : previous,
    );
  };
  const validate = useMutation({
    mutationFn: () => templatesApi.validate(id),
    onSuccess: applyReport,
  });
  const generate = useMutation({
    mutationFn: async () => {
      const report = await templatesApi.validate(id);
      applyReport(report);
      if (
        !report.ready ||
        report.checks?.some((check) => check.category === 'blocking' && check.result === 'failed')
      )
        throw new ApiError(422, report);
      return templatesApi.generate(id, draft!.revision);
    },
    onSuccess: (result) => {
      if (draft && isCurrentOwner(draft.owner_id))
        navigate(`/templates/results/${result.generated_document_id}`);
    },
    onError: (error) => {
      const report = validationFromError(error);
      if (report) applyReport(report);
    },
  });
  const editing = selected !== null;
  const edit = (key: string) => {
    setSelected(key);
    setReview(false);
    setSaved('');
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };
  const onSaved = (updated: TemplateDraft) => {
    // A response arriving after logout/account switching must not resurrect
    // private query data that clearSession has already removed.
    if (!isCurrentOwner(updated.owner_id)) return;
    client.setQueryData(templateKeys.draft(id), updated);
    void client.invalidateQueries({ queryKey: ['templates', 'drafts'] });
    setSaved(`Ответ сохранён · версия ${updated.revision}`);
    setSelected(null);
    setReview(editing);
    if (!updated.validation) void query.refetch();
  };
  return {
    query,
    draft,
    template,
    review,
    setReview,
    selected,
    setSelected,
    saved,
    generateLock,
    validate,
    generate,
    edit,
    onSaved,
  };
}
