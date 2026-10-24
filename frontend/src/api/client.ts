export const API_BASE = '/api/v1';
export const SESSION_EXPIRED_EVENT = 'assistant:session-expired';

const errorMessages: Record<number, string> = {
  400: 'Проверьте введённые данные. Сервер не смог обработать запрос.',
  401: 'Сессия завершена. Войдите в аккаунт ещё раз.',
  403: 'Доступ запрещён. Проверьте права доступа или обновите страницу. Если ошибка повторяется, обратитесь к администратору.',
  404: 'Запрошенные данные не найдены или больше недоступны.',
  405: 'Это действие пока не поддерживается сервером.',
  409: 'Данные изменились или диалог архивирован. Обновите страницу и повторите запрос.',
  413: 'Файл превышает допустимый сервером размер.',
  422: 'Проверьте обязательные поля и блокирующие замечания.',
  429: 'Слишком много запросов. Подождите немного и попробуйте снова.',
  500: 'Не удалось обработать запрос. Попробуйте ещё раз позже.',
  501: 'Эта возможность ещё не реализована на сервере.',
  502: 'Не удалось связаться с сервером. Попробуйте ещё раз позже.',
  503: 'Сервис временно недоступен. Попробуйте ещё раз позже.',
  504: 'Сервер не успел ответить. Попробуйте ещё раз.',
};

export class ApiError extends Error {
  constructor(public readonly status: number, public readonly details?: unknown) {
    super(errorMessages[status] ?? 'Произошла ошибка при обращении к серверу.');
    this.name = 'ApiError';
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return 'Не удалось связаться с сервером. Проверьте подключение и повторите запрос.';
}

export function readCsrfToken(cookies = document.cookie): string | undefined {
  for (const name of ['__Host-ai_csrf', 'ai_csrf']) {
    const part = cookies
      .split(';')
      .map((item) => item.trim())
      .find((item) => item.startsWith(`${name}=`));
    if (part) {
      try {
        return decodeURIComponent(part.slice(name.length + 1));
      } catch {
        return undefined;
      }
    }
  }
}

type Method = 'GET' | 'POST' | 'PATCH' | 'DELETE';
interface RequestOptions {
  method?: Method;
  body?: unknown;
  signal?: AbortSignal;
}

// JSON and multipart share authentication, cancellation and safe error handling.
export async function apiFetch(path: string, options: RequestOptions = {}): Promise<Response> {
  const method = options.method ?? 'GET';
  const headers = new Headers({ Accept: 'application/json' });
  let body: BodyInit | undefined;
  if (options.body !== undefined) {
    if (options.body instanceof FormData) body = options.body;
    else {
      headers.set('Content-Type', 'application/json');
      body = JSON.stringify(options.body);
    }
  }
  if (method !== 'GET') {
    const csrf = readCsrfToken();
    if (csrf) headers.set('X-CSRF-Token', csrf);
  }
  const response = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body,
    signal: options.signal,
    credentials: 'same-origin',
    cache: 'no-store',
  });
  if (!response.ok) {
    if (response.status === 401 && path !== '/auth/login' && path !== '/auth/me') {
      window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
    }
    // Server error text is deliberately not rendered: it may contain internal details.
    const details: unknown = await response.json().catch(() => undefined);
    throw new ApiError(response.status, details);
  }
  return response;
}

export async function apiRequest<T = void>(path: string, options: RequestOptions = {}): Promise<T> {
  const response = await apiFetch(path, options);
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  if (!text.trim()) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(502);
  }
}
