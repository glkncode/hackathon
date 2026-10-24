import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRef, useState, type ChangeEvent, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../features/auth';
import { documentsApi } from '../../features/documents';
import type { TemplateProfile } from '../../features/templates';
import { templatesApi } from '../../features/templates';
import { ApiError } from '../../shared/api/client';

export function useTemplateRegistration() {
  const user = useAuth();
  const navigate = useNavigate();
  const client = useQueryClient();
  const [documentId, setDocumentId] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [profile, setProfile] = useState<TemplateProfile>();
  const [fileError, setFileError] = useState('');
  const registerLock = useRef(false);
  const documents = useInfiniteQuery({
    queryKey: ['template-source-documents'],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => documentsApi.list(pageParam, signal),
    getNextPageParam: (p, pages) => (p.items?.length === 20 ? pages.length * 20 : undefined),
    enabled: user.data?.role === 'admin',
  });
  const source = useQuery({
    queryKey: ['template-source-document', documentId],
    queryFn: ({ signal }) => documentsApi.get(documentId, signal),
    enabled: !!documentId && user.data?.role === 'admin',
  });
  const register = useMutation({
    mutationFn: () =>
      templatesApi.register({
        document_id: documentId,
        document_version_id: source.data!.latest_version.id,
        name,
        description,
        profile: profile!,
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['templates'] });
      navigate('/templates');
    },
  });

  const needsConfiguration =
    register.error instanceof ApiError &&
    typeof register.error.details === 'object' &&
    register.error.details !== null &&
    'support_status' in register.error.details &&
    register.error.details.support_status === 'needs_configuration';
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (
      !registerLock.current &&
      !register.isPending &&
      profile &&
      source.data &&
      !source.isFetching
    ) {
      registerLock.current = true;
      register.mutate(undefined, {
        onSettled: () => {
          registerLock.current = false;
        },
      });
    }
  };
  const readProfile = async (e: ChangeEvent<HTMLInputElement>) => {
    setProfile(undefined);
    setFileError('');
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      if (file.size > 196608) throw new Error();
      const parsed: unknown = JSON.parse(await file.text());
      if (
        !parsed ||
        typeof parsed !== 'object' ||
        !('profile_version' in parsed) ||
        parsed.profile_version !== 2 ||
        !('fields' in parsed) ||
        !Array.isArray(parsed.fields)
      )
        throw new Error();
      setProfile(parsed as TemplateProfile);
    } catch {
      setFileError(
        'Не удалось прочитать профиль v2. Выберите подготовленный JSON-файл размером до 192 КиБ.',
      );
    }
  };
  return {
    user,
    documentId,
    setDocumentId,
    name,
    setName,
    description,
    setDescription,
    profile,
    fileError,
    documents,
    source,
    register,
    needsConfiguration,
    submit,
    readProfile,
  };
}
