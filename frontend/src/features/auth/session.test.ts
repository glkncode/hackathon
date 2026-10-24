import { describe, expect, it } from 'vitest';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { authKey, clearSession } from '../../app/providers';

describe('private cache isolation', () => {
  it('notifies an active auth observer and removes private data on session expiry', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(authKey, { id: 'user', username: 'member', role: 'member' });
    client.setQueryData(['documents', 'list'], { items: ['private document'] });
    client.setQueryData(['chat', 'history', 'session'], { messages: ['private content'] });
    // Use the public observer API to ensure existing mounted routes get updated.
    const activeObserver = new QueryObserver(client, { queryKey: authKey, enabled: false });
    const received: unknown[] = [];
    const unsubscribe = activeObserver.subscribe((result) => received.push(result.data));
    await clearSession(client);
    expect(received).toContain(null);
    expect(client.getQueryData(authKey)).toBeNull();
    expect(client.getQueryData(['documents', 'list'])).toBeUndefined();
    expect(client.getQueryData(['chat', 'history', 'session'])).toBeUndefined();
    unsubscribe();
    client.clear();
  });
});
