// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { authKey } from '../../features/auth';
import { chatApi } from '../../features/chat';
import { AppLayout } from './AppLayout';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('traps mobile menu focus with module classes and restores focus on Escape', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  // jsdom has no layout; treat sidebar controls as visible at mobile width.
  const rect = new DOMRect(0, 0, 1, 1);
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue(
    Object.assign([rect], { item: () => rect }),
  );
  vi.spyOn(chatApi, 'list').mockResolvedValue({ sessions: [], limit: 20, offset: 0 });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(authKey, { id: 'member', username: 'tester', role: 'member' });
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <MemoryRouter>
            <AppLayout />
          </MemoryRouter>
        </QueryClientProvider>,
      ),
    );
    const toggle = host.querySelector<HTMLButtonElement>('button[aria-label="Открыть меню"]')!;
    toggle.focus();
    await act(async () => toggle.click());
    const sidebar = host.querySelector<HTMLElement>('#main-sidebar')!;
    const controls = [...sidebar.querySelectorAll<HTMLElement>('a, button:not(:disabled)')];
    expect(document.activeElement).toBe(controls[0]);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(toggle.closest('[inert]')).not.toBeNull();
    await act(async () =>
      document.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, cancelable: true }),
      ),
    );
    expect(document.activeElement).toBe(controls.at(-1));
    await act(async () =>
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', cancelable: true })),
    );
    expect(document.activeElement).toBe(controls[0]);
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })));
    expect(document.activeElement).toBe(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(toggle.closest('[inert]')).toBeNull();
    expect(chatApi.list).toHaveBeenCalledTimes(1);
  } finally {
    await act(async () => root.unmount());
    client.clear();
    host.remove();
  }
});
