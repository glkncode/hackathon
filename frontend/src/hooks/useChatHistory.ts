import { queryOptions, useQuery } from '@tanstack/react-query';
import { chatApi } from '../api/chat';

export function chatHistoryOptions(sessionId: string | null) {
  return queryOptions({
    queryKey: ['chat', 'history', sessionId] as const,
    queryFn: ({ signal }) => chatApi.history(sessionId!, signal),
    enabled: !!sessionId,
    // Reopening must recheck the server's current content and document access.
    staleTime: 0,
    gcTime: 0,
    refetchOnMount: 'always',
  });
}

export function useChatHistory(sessionId: string | null) {
  return useQuery(chatHistoryOptions(sessionId));
}
