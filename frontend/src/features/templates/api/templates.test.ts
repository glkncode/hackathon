import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../../shared/api/client';
import { saveDownload } from '../../../shared/lib/download';
import { DOCX_MIME, templatesApi, validationFromError } from './templates';

vi.mock('../../../shared/lib/download', () => ({ saveDownload: vi.fn() }));
const fetchMock = vi.fn<typeof fetch>();
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('document', { cookie: 'ai_csrf=token' });
  vi.stubGlobal('window', new EventTarget());
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
describe('template transport contract', () => {
  it('preserves structured validation on 422, keeping internal error text private', async () => {
    const report = { ready: false, missing_fields: [], invalid_fields: [], next_question: null };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(report), { status: 422 }));
    try {
      await templatesApi.generate('draft', 7);
      throw new Error('expected rejection');
    } catch (error) {
      expect(validationFromError(error)).toEqual(report);
    }
    expect(validationFromError(new ApiError(422, { error: 'secret' }))).toBeUndefined();
  });
  it('sends expected revision and uses same body for idempotent retries', async () => {
    fetchMock.mockImplementation(async () => new Response('{}'));
    await templatesApi.generate('draft', 7);
    await templatesApi.generate('draft', 7);
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBe('{"expected_revision":7}');
    expect(fetchMock.mock.calls[1]?.[1]?.body).toBe(fetchMock.mock.calls[0]?.[1]?.body);
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('X-CSRF-Token')).toBe('token');
  });
  it.each([401, 403, 404, 502])('never downloads HTTP %s JSON as DOCX', async (status) => {
    fetchMock.mockResolvedValue(new Response('{"error":"denied"}', { status }));
    await expect(templatesApi.download('file', 'x.docx')).rejects.toMatchObject({ status });
    expect(saveDownload).not.toHaveBeenCalled();
  });
  it('rejects successful JSON, partial files or missing disposition', async () => {
    for (const response of [
      new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
      new Response('x', {
        status: 206,
        headers: {
          'Content-Type': DOCX_MIME,
          'Content-Disposition': 'attachment; filename=x.docx',
        },
      }),
      new Response('x', { headers: { 'Content-Type': DOCX_MIME } }),
    ]) {
      fetchMock.mockResolvedValue(response);
      await expect(templatesApi.download('file', 'x.docx')).rejects.toMatchObject({ status: 502 });
    }
    expect(saveDownload).not.toHaveBeenCalled();
  });
  it('passes protected MIME-checked response to safe filename/object URL saver', async () => {
    fetchMock.mockResolvedValue(
      new Response('PK', {
        headers: {
          'Content-Type': DOCX_MIME,
          'Content-Disposition': "attachment; filename*=UTF-8''project.docx",
        },
      }),
    );
    await templatesApi.download('file', 'fallback.docx');
    expect(saveDownload).toHaveBeenCalled();
    expect(fetchMock.mock.calls[0]?.[1]?.credentials).toBe('same-origin');
  });
});
