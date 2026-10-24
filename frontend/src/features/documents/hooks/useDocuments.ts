import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { documentsApi } from '../api/documents';
import { documentsKey } from './keys';

export function useDocuments() {
  const client = useQueryClient();
  const [offset, setOffset] = useState(0);
  const documents = useQuery({
    queryKey: [...documentsKey, 'list', offset],
    queryFn: ({ signal }) => documentsApi.list(offset, signal),
  });
  return {
    documents,
    offset,
    setOffset,
    refresh: () => client.invalidateQueries({ queryKey: documentsKey }),
  };
}
