// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Sidebar } from '../../app/layout/Sidebar';
import { AdminRoute, authKey } from '../../features/auth';
import { chatApi } from '../../features/chat';
import { type ListedUser } from '../../features/users';
import { UsersPage } from './UsersPage';

const admin: ListedUser = { id: 'admin', username: 'administrator', role: 'admin', is_active: true };
const member: ListedUser = { id: 'member', username: 'participant', role: 'member', is_active: true };
const inactive: ListedUser = { ...member, id: 'inactive', username: 'disabled', is_active: false };
const password = 'fixture-password-123';
let host: HTMLDivElement;
let root: Root;
let client: QueryClient;
let accounts: ListedUser[];
let allowed: boolean;
let postStatus: number;
let aclStatus: number;
const fetchMock = vi.fn<typeof fetch>();
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });

beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  accounts = [admin, member, inactive];
  allowed = false;
  postStatus = 201;
  aclStatus = 200;
  document.cookie = 'ai_csrf=fixture-csrf; path=/';
  fetchMock.mockImplementation(async (input, options) => {
    const url = String(input);
    if (url === '/api/v1/users' && options?.method === 'POST') {
      if (postStatus !== 201) return json({ error: password }, postStatus);
      if (new Headers(options.headers).get('X-CSRF-Token') !== 'fixture-csrf') return json({ error: 'csrf' }, 403);
      const { username } = JSON.parse(String(options.body));
      const created: ListedUser = { ...member, id: 'created', username };
      accounts = [...accounts, created];
      return json(created, 201);
    }
    if (url.startsWith('/api/v1/users?')) {
      const offset = Number(new URL(url, 'http://localhost').searchParams.get('offset'));
      return json({ items: accounts.slice(offset, offset + 20), total: accounts.length });
    }
    if (url.startsWith('/api/v1/documents?')) return json({ items: [{ id: 'doc', title: 'Регламент' }], total: 1 });
    if (url.includes('/documents/doc/access')) {
      if (aclStatus !== 200) return json({ error: password }, aclStatus);
      if (options?.method === 'POST') { allowed = true; return json({}, 201); }
      if (options?.method === 'DELETE') { allowed = false; return new Response(null, { status: 204 }); }
      return json({ items: allowed ? [member] : [], total: allowed ? 1 : 0 });
    }
    throw new Error(`Unexpected request: ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.spyOn(chatApi, 'list').mockResolvedValue({ sessions: [], limit: 20, offset: 0 });
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  client.clear();
  host.remove();
  document.cookie = 'ai_csrf=; max-age=0; path=/';
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  fetchMock.mockReset();
});
async function settle() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
}
async function render(user: ListedUser | null = admin, route = true) {
  client.setQueryData(authKey, user);
  await act(async () => root.render(
    <QueryClientProvider client={client}><MemoryRouter initialEntries={['/users']}>
      <Sidebar open={false} onClose={() => {}} />
      {route ? <Routes><Route element={<AdminRoute />}><Route path="/users" element={<UsersPage />} /></Route></Routes> : <UsersPage />}
    </MemoryRouter></QueryClientProvider>,
  ));
  await settle();
}
async function submit(username = 'new.member', secret = password) {
  host.querySelector<HTMLInputElement>('#new-username')!.value = username;
  host.querySelector<HTMLInputElement>('#new-password')!.value = secret;
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  await settle();
}
async function click(name: string) {
  const button = [...host.querySelectorAll('button')].find((item) => item.textContent === name || item.getAttribute('aria-label') === name)!;
  expect(button).toBeDefined();
  await act(async () => button.click());
  await settle();
}
const posts = () => fetchMock.mock.calls.filter(([url, options]) => url === '/api/v1/users' && options?.method === 'POST');

describe('Users administration RBAC', () => {
  it.each([member, null])('hides navigation and denies direct route to non-admin (%s)', async (user) => {
    await render(user);
    expect(host.querySelector('a[href="/users"]')).toBeNull();
    expect(host.textContent).toContain('Доступ запрещён');
    expect(host.querySelector('#new-username')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it('also guards a directly mounted UsersPage', async () => {
    await render(member, false);
    expect(host.querySelector('#new-username')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it('shows admin navigation and keeps actual account roles/activity', async () => {
    await render();
    expect(host.querySelector('a[href="/users"]')?.textContent).toContain('Пользователи');
    expect(host.textContent).toContain('Администратор');
    expect(host.textContent).toContain('Участник');
    expect(host.textContent).toContain('Неактивен');
    expect(host.querySelector('input[name="role"]')).toBeNull();
    expect(host.querySelector('input[name="password"]')?.getAttribute('type')).toBe('password');
  });
  it('renders server access denial without retaining stale list', async () => {
    fetchMock.mockResolvedValue(json({ error: 'forbidden' }, 403));
    await render();
    expect(host.textContent).toContain('Доступ запрещён');
    expect(host.querySelector('table')).toBeNull();
  });
});

describe('member creation with a visual job role', () => {
  it('creates an account with CSRF and opens access settings without sending the visual role', async () => {
    await render();
    const role = host.querySelector<HTMLSelectElement>('#new-role')!;
    expect([...role.options].slice(1).map((option) => option.text)).toEqual([
      'Бухгалтер', 'Менеджер', 'Юрист', 'Кадровик', 'Делопроизводитель',
    ]);
    const requestsBefore = fetchMock.mock.calls.length;
    role.value = 'accountant';
    await act(async () => role.dispatchEvent(new Event('change', { bubbles: true })));
    await settle();
    expect(fetchMock.mock.calls).toHaveLength(requestsBefore);
    await submit();
    const options = posts()[0]![1]!;
    expect(posts()).toHaveLength(1);
    expect(JSON.parse(String(options.body))).toEqual({ username: 'new.member', password });
    expect(options.credentials).toBe('same-origin');
    expect(new Headers(options.headers).get('X-CSRF-Token')).toBe('fixture-csrf');
    expect(fetchMock.mock.calls.filter(([url]) => String(url).startsWith('/api/v1/users?'))).toHaveLength(2);
    expect(host.querySelector('tbody')?.textContent).toContain('new.member');
    expect(host.querySelector('tbody')?.textContent).not.toContain('Бухгалтер');
    expect(accounts.at(-1)?.role).toBe('member');
    expect(host.textContent).toContain('Доступ к документам: new.member');
    expect(host.querySelector<HTMLInputElement>('#new-password')?.value).toBe('');
    expect(host.textContent).not.toContain(password);
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.variables))).not.toContain(password);
  });
  it('opens the last page after creation because the server lists oldest accounts first', async () => {
    accounts = Array.from({ length: 20 }, (_, i) => ({ ...member, id: `member-${i}`, username: `participant-${i}` }));
    await render();
    await submit();
    await settle();
    expect(host.querySelector('tbody')?.textContent).toContain('new.member');
    expect(fetchMock.mock.calls.some(([url]) => url === '/api/v1/users?limit=20&offset=20')).toBe(true);
  });
  it.each([
    ['ab', password, 'Логин:'],
    ['member admin', password, 'Логин:'],
    ['valid', 'short', 'Пароль:'],
    ['valid', 'я'.repeat(513), 'Пароль:'],
  ])('validates %s without a request', async (username, secret, message) => {
    await render();
    await submit(username, secret);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(message);
    expect(posts()).toHaveLength(0);
  });
  it.each([[400, 'Проверьте логин и пароль'], [409, 'логин уже занят'], [403, 'Доступ запрещён'], [500, 'Попробуйте ещё раз позже']])(
    'shows safe HTTP %s error and clears the password', async (status, message) => {
      postStatus = Number(status);
      await render();
      await submit();
      expect(host.querySelector('[role="alert"]')?.textContent).toContain(message);
      expect(host.textContent).not.toContain(password);
      expect(host.querySelector<HTMLInputElement>('#new-password')?.value).toBe('');
      expect(host.textContent).not.toContain('Пользователь создан');
      expect(posts()).toHaveLength(1);
    },
  );
  it('shows CSRF denial when the readable cookie is missing', async () => {
    document.cookie = 'ai_csrf=; max-age=0; path=/';
    await render();
    await submit();
    expect(new Headers(posts()[0]![1]?.headers).has('X-CSRF-Token')).toBe(false);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Доступ запрещён');
    expect(accounts).toHaveLength(3);
  });
});

describe('selected user document access', () => {
  it('grants and revokes using existing endpoints and refreshes current permissions', async () => {
    await render();
    await click('Настроить доступ: participant');
    await click('Выдать доступ: Регламент');
    expect(host.textContent).toContain('Доступ разрешён');
    await click('Отозвать доступ: Регламент');
    expect(host.textContent).toContain('Нет доступа');
    const writes = fetchMock.mock.calls.filter(([url, options]) => String(url).includes('/access') && options?.method !== 'GET');
    expect(writes).toHaveLength(2);
    expect(writes[0]?.[0]).toBe('/api/v1/documents/doc/access');
    expect(JSON.parse(String(writes[0]?.[1]?.body))).toEqual({ user_id: 'member' });
    expect(writes[1]?.[0]).toBe('/api/v1/documents/doc/access/member');
    for (const [, options] of writes) expect(new Headers(options?.headers).get('X-CSRF-Token')).toBe('fixture-csrf');
  });
  it('does not guess permissions or enable grants after ACL list failure', async () => {
    aclStatus = 403;
    await render();
    await click('Настроить доступ: participant');
    expect(host.querySelector('button[aria-label="Выдать доступ: Регламент"]')).toBeNull();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Доступ запрещён');
  });
  it('keeps the old permission when a mutation fails', async () => {
    await render();
    await click('Настроить доступ: participant');
    aclStatus = 403;
    await click('Выдать доступ: Регламент');
    expect(host.textContent).toContain('Нет доступа');
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Доступ запрещён');
  });
});
