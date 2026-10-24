import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authApi } from '../../features/auth';
import { chatApi, isChatUnavailable } from '../../features/chat';
import { documentsApi } from '../../features/documents';
import {
  ApiError,
  apiRequest,
  readCsrfToken,
  SESSION_EXPIRED_EVENT,
} from '../../shared/api/client';

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('document', { cookie: 'ai_csrf=csrf-test' });
  vi.stubGlobal('window', new EventTarget());
});
afterEach(() => {
  vi.unstubAllGlobals();
  fetchMock.mockReset();
});

describe('session and CSRF transport', () => {
  it('selects the production CSRF cookie without touching the session cookie', () => {
    expect(readCsrfToken('ai_session=secret; ai_csrf=dev; __Host-ai_csrf=prod%2Dtoken')).toBe(
      'prod-token',
    );
    expect(readCsrfToken('ai_session=secret')).toBeUndefined();
    expect(readCsrfToken('ai_csrf=%XX')).toBeUndefined();
  });

  it('sends same-origin cookies, JSON, CSRF and the caller AbortSignal', async () => {
    fetchMock.mockResolvedValue(new Response('{"total":0}'));
    const controller = new AbortController();
    await apiRequest('/search', {
      method: 'POST',
      body: { query: 'регламент', limit: 10 },
      signal: controller.signal,
    });
    const [url, options] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/v1/search');
    expect(options?.credentials).toBe('same-origin');
    expect(options?.signal).toBe(controller.signal);
    expect(options?.body).toBe('{"query":"регламент","limit":10}');
    const headers = new Headers(options?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('csrf-test');
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  it('does not set a multipart boundary or Content-Type itself', async () => {
    fetchMock.mockResolvedValue(new Response('{}', { status: 201 }));
    await documentsApi.upload('Правила', new File(['text'], 'rules.txt', { type: 'text/plain' }));
    const options = fetchMock.mock.calls[0]![1]!;
    expect(new Headers(options.headers).has('Content-Type')).toBe(false);
    expect(new Headers(options.headers).get('X-CSRF-Token')).toBe('csrf-test');
    expect(options.body).toBeInstanceOf(FormData);
    expect((options.body as FormData).get('title')).toBe('Правила');
  });

  it('handles logout with an empty 204 and CSRF', async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(authApi.logout()).resolves.toBeUndefined();
    expect(new Headers(fetchMock.mock.calls[0]![1]?.headers).get('X-CSRF-Token')).toBe('csrf-test');
  });

  it('does not attach CSRF to a read', async () => {
    fetchMock.mockResolvedValue(new Response('{}'));
    await apiRequest('/documents');
    expect(new Headers(fetchMock.mock.calls[0]![1]?.headers).has('X-CSRF-Token')).toBe(false);
  });

  it('returns an anonymous user for /auth/me 401 without retry', async () => {
    fetchMock.mockResolvedValue(new Response('', { status: 401 }));
    await expect(authApi.me()).resolves.toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('expires private UI on protected 401, but allows invalid login credentials', async () => {
    const expired = vi.fn();
    window.addEventListener(SESSION_EXPIRED_EVENT, expired);
    fetchMock.mockImplementation(async () => new Response('', { status: 401 }));
    await expect(authApi.login('test', 'test')).rejects.toMatchObject({ status: 401 });
    expect(expired).not.toHaveBeenCalled();
    await expect(apiRequest('/documents')).rejects.toMatchObject({ status: 401 });
    expect(expired).toHaveBeenCalledTimes(1);
  });
});

describe('safe API failures', () => {
  it.each([400, 401, 403, 404, 409, 413, 429, 500, 503])(
    'maps HTTP %s without exposing the backend error body',
    async (status) => {
      fetchMock.mockResolvedValue(
        new Response('{"error":"private SQL trace or secret"}', { status }),
      );
      let caught: unknown;
      try {
        await apiRequest('/documents');
      } catch (error) {
        caught = error;
      }
      expect(caught).toBeInstanceOf(ApiError);
      expect(caught).toMatchObject({ status });
      expect((caught as Error).message).not.toContain('private SQL');
      expect(fetchMock).toHaveBeenCalledTimes(1);
    },
  );

  it('rejects an unexpected non-JSON success', async () => {
    fetchMock.mockResolvedValue(new Response('<html>proxy error</html>'));
    await expect(apiRequest('/documents')).rejects.toMatchObject({ status: 502 });
  });

  it('preserves network cancellation', async () => {
    const abort = new DOMException('Aborted', 'AbortError');
    fetchMock.mockRejectedValue(abort);
    await expect(apiRequest('/documents')).rejects.toBe(abort);
  });
});

describe('documented Chat API', () => {
  it('only permits search fallback for a missing chat capability', () => {
    for (const status of [404, 405, 501])
      expect(isChatUnavailable(new ApiError(status))).toBe(true);
    for (const status of [401, 403, 409, 500, 503])
      expect(isChatUnavailable(new ApiError(status))).toBe(false);
    expect(isChatUnavailable(new TypeError('network error'))).toBe(false);
  });

  it('loads all chronological history pages rather than truncating after 100 messages', async () => {
    fetchMock
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            session: { id: 'session' },
            messages: Array.from({ length: 100 }, (_, i) => ({ id: String(i) })),
            limit: 100,
            offset: 0,
          }),
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ messages: [{ id: '100' }], limit: 100, offset: 100 })),
      );
    const history = await chatApi.history('session');
    expect(history.messages).toHaveLength(101);
    expect(fetchMock.mock.calls[1]?.[0]).toContain('offset=100');
    expect(history.messages.at(-1)?.id).toBe('100');
  });

  it('passes the caller idempotency key unchanged when retrying a message', async () => {
    fetchMock.mockImplementation(async () => new Response('{}'));
    await chatApi.send('session', 'Согласование', 'client-key');
    await chatApi.send('session', 'Согласование', 'client-key');
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBe(
      '{"content":"Согласование","client_message_id":"client-key"}',
    );
    expect(fetchMock.mock.calls[1]?.[1]?.body).toBe(fetchMock.mock.calls[0]?.[1]?.body);
  });
});
