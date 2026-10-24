// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { saveDownload } from './download';

let clicked: { name: string; href: string }[];
const revoke = vi.fn();
beforeEach(() => {
  vi.useFakeTimers(); clicked = [];
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = vi.fn(() => 'blob:test-docx');
    static revokeObjectURL = revoke;
  });
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push({ name: this.download, href: this.href });
  });
});
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.clearAllMocks(); });

describe('protected file delivery', () => {
  it('decodes the server UTF-8 filename, removes the anchor and revokes the object URL', async () => {
    await saveDownload(new Response('PK', { headers: { 'Content-Disposition': "attachment; filename*=UTF-8''%D0%9F%D1%80%D0%BE%D0%B5%D0%BA%D1%82.docx" } }), 'fallback.docx');
    expect(clicked).toEqual([{ name: 'Проект.docx', href: 'blob:test-docx' }]);
    expect(document.querySelector('a')).toBeNull(); expect(revoke).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1000); expect(revoke).toHaveBeenCalledWith('blob:test-docx');
  });
  it('sanitizes directory separators in filenames', async () => {
    await saveDownload(new Response('PK', { headers: { 'Content-Disposition': 'attachment; filename="../../project.docx"' } }), 'fallback.docx');
    expect(clicked[0]?.name).toBe('.._.._project.docx');
  });
  it('uses the supplied filename when RFC5987 encoding is malformed', async () => {
    await saveDownload(new Response('PK', { headers: { 'Content-Disposition': "attachment; filename*=UTF-8''%INVALID" } }), 'safe.docx');
    expect(clicked[0]?.name).toBe('safe.docx');
  });
});
