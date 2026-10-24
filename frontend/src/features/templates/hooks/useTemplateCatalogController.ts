import { useMutation } from '@tanstack/react-query';
import { useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { templatesApi } from '../api/templates';
import { useTemplateCatalog, useTemplateDrafts } from './useTemplates';

export function useTemplateCatalogController() {
  const catalog = useTemplateCatalog();
  const drafts = useTemplateDrafts();
  const navigate = useNavigate();
  const createLock = useRef(false);
  const create = useMutation({
    mutationFn: templatesApi.create,
    onSuccess: (draft) => navigate(`/templates/drafts/${draft.id}`),
  });
  return { catalog, drafts, createLock, create };
}
