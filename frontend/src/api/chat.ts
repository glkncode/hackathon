// Contract: docs/chat.md and internal/transport/http/handlers/chat.go.
import { ApiError, apiRequest } from './client';
import type { ChatHistory, ChatSession, ChatSessions, ChatTurn } from '../types/api';

export const isChatUnavailable = (error: unknown) =>
  error instanceof ApiError && [404, 405, 501].includes(error.status);

export const chatApi = {
  list: (offset = 0, signal?: AbortSignal) =>
    apiRequest<ChatSessions>(`/chat/sessions?limit=20&offset=${offset}`, { signal }),
  create: (title: string, signal?: AbortSignal) =>
    apiRequest<ChatSession>('/chat/sessions', { method: 'POST', body: { title }, signal }),
  get: (id: string, offset = 0, signal?: AbortSignal) =>
    apiRequest<ChatHistory>(`/chat/sessions/${encodeURIComponent(id)}?limit=100&offset=${offset}`, {
      signal,
    }),
  send: (id: string, content: string, clientMessageId: string, signal?: AbortSignal) =>
    apiRequest<ChatTurn>(`/chat/sessions/${encodeURIComponent(id)}/messages`, {
      method: 'POST',
      body: { content, client_message_id: clientMessageId },
      signal,
    }),
  archive: (id: string) =>
    apiRequest(`/chat/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  async history(id: string, signal?: AbortSignal): Promise<ChatHistory> {
    const first = await chatApi.get(id, 0, signal);
    const messages = [...first.messages];
    let page = first;
    while (page.messages.length === 100) {
      page = await chatApi.get(id, messages.length, signal);
      messages.push(...page.messages);
    }
    return { ...first, messages };
  },
};
