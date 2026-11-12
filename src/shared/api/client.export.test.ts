import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError, NetworkApiError, createCanonKeeperApiClient } from './index';

function answer(status: number, body: BodyInit, headers: Record<string, string>) {
  return vi.fn(async (_request: Request) => new Response(body, { headers, status }));
}

describe('getBookExport', () => {
  afterEach(() => vi.restoreAllMocks());

  it('takes the file and the name the server gave it, as RFC 6266 writes a name that is not ASCII', async () => {
    const fetch = answer(200, 'файл', { 'Content-Disposition': `attachment; filename*=utf-8''${encodeURIComponent('Том 1_ «Порт».docx')}` });
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });

    const file = await client.getBookExport('book-1', 'docx');

    expect(file.fileName).toBe('Том 1_ «Порт».docx');
    expect(await file.blob.text()).toBe('файл');
    const request = fetch.mock.calls[0]![0];
    expect(request.method).toBe('GET');
    expect(request.url).toBe('http://test.local/api/v1/books/book-1/export?format=docx');
  });

  // specs/2026-11-12-ux10-book-pictures.md BP3.1: the drawings of the book are asked by drawings=true; without them nothing is sent.
  it('asks the drawings of the book only when they are wanted', async () => {
    const fetch = answer(200, 'файл', {});
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });
    await client.getBookExport('book-1', 'epub', { drawings: true });
    await client.getBookExport('book-1', 'epub', { drawings: false });
    expect(fetch.mock.calls.map(([request]) => request.url)).toEqual([
      'http://test.local/api/v1/books/book-1/export?format=epub&drawings=true',
      'http://test.local/api/v1/books/book-1/export?format=epub',
    ]);
  });

  it('reads a plain name and falls back to one of its own', async () => {
    const plain = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch: answer(200, 'x', { 'Content-Disposition': 'attachment; filename="My Book.epub"' }) });
    expect((await plain.getBookExport('book-1', 'epub')).fileName).toBe('My Book.epub');
    const unnamed = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch: answer(200, 'x', {}) });
    expect((await unnamed.getBookExport('book-1', 'fb2')).fileName).toBe('Книга.fb2');
  });

  it('turns a refusal into a problem with its code', async () => {
    const client = createCanonKeeperApiClient({
      baseUrl: 'http://test.local/api/v1',
      fetch: answer(422, JSON.stringify({ status: 422, code: 'book_export.empty', title: 'Unprocessable Entity', detail: 'no text' }), { 'Content-Type': 'application/problem+json' }),
    });
    const error = await client.getBookExport('book-1', 'docx').catch((failure: unknown) => failure);
    expect(error).toBeInstanceOf(ApiStatusError);
    expect((error as ApiStatusError).problem.code).toBe('book_export.empty');
  });

  it('turns a broken connection into a network failure: half a file is never a success', async () => {
    const client = createCanonKeeperApiClient({
      baseUrl: 'http://test.local/api/v1',
      fetch: vi.fn(async () => { throw new TypeError('network error'); }),
    });
    await expect(client.getBookExport('book-1', 'docx')).rejects.toBeInstanceOf(NetworkApiError);
  });
});

// specs/2026-11-12-ux10-book-pictures.md BP3.1, BP3.2: how many drawings the files of the books of a saga would carry.
describe('getBookDrawingCounts', () => {
  it('reads the drawings of the books of a saga', async () => {
    const fetch = vi.fn(async (_request: Request) => Response.json({ data: [{ bookId: 'book-1', drawings: 2 }] }));
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });
    expect(await client.getBookDrawingCounts('project-1')).toEqual({ data: [{ bookId: 'book-1', drawings: 2 }] });
    expect(fetch.mock.calls[0]![0].url).toBe('http://test.local/api/v1/projects/project-1/books/drawing-counts');
  });
});
