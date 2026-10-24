import { useInfiniteQuery } from '@tanstack/react-query';
import { chatApi, isChatUnavailable } from '../api/chat';

export const sessionsKey = ['chat', 'sessions'] as const;

export function useChatSessions() {
  return useInfiniteQuery({
    queryKey: sessionsKey,
    initialPageParam: 0,
    queryFn: async ({ pageParam, signal }) => {
      try {
        return { ...(await chatApi.list(pageParam, signal)), available: true };
      } catch (error) {
        // Only a missing capability enables fallback; outages remain visible.
        if (pageParam === 0 && isChatUnavailable(error))
          return { sessions: [], limit: 20, offset: 0, available: false };
        throw error;
      }
    },
    getNextPageParam: (last) =>
      last.sessions.length === last.limit ? last.offset + last.limit : undefined,
  });
}
