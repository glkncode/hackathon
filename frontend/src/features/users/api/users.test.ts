import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { usersApi } from './users';

const fetchMock = vi.fn<typeof fetch>();
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('document', { cookie: '__Host-ai_csrf=production-csrf' });
});
afterEach(() => { vi.unstubAllGlobals(); fetchMock.mockReset(); });

it('checks ACL pages beyond the first 100 users and propagates cancellation', async () => {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ items: Array.from({ length: 100 }, (_, i) => ({ id: String(i) })), total: 101 })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ items: [{ id: 'target' }], total: 101 })));
  const controller = new AbortController();
  expect(await usersApi.hasAccess('doc', 'target', controller.signal)).toBe(true);
  expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/v1/documents/doc/access?limit=100&offset=100');
  for (const [, options] of fetchMock.mock.calls) {
    expect(options?.signal).toBe(controller.signal);
    expect(new Headers(options?.headers).has('X-CSRF-Token')).toBe(false);
  }
});

it('never turns an incomplete ACL list into a grant decision', async () => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [], total: 100 })));
  await expect(usersApi.hasAccess('doc', 'target')).rejects.toThrow('Incomplete ACL response');
});

it('uses the production CSRF cookie for creation and both ACL writes', async () => {
  fetchMock.mockImplementation(async () => new Response('{}'));
  await usersApi.create('member', 'fixture-password-123');
  await usersApi.grant('doc', 'target');
  await usersApi.revoke('doc', 'target');
  for (const [, options] of fetchMock.mock.calls) {
    expect(options?.credentials).toBe('same-origin');
    expect(new Headers(options?.headers).get('X-CSRF-Token')).toBe('production-csrf');
  }
});
