import { describe, expect, it, vi } from 'vitest';
import { readAllPages } from './pages';

describe('readAllPages', () => {
  it('joins every page in the order of the server', async () => {
    const pages = {
      '': { data: [1, 2], meta: { hasMore: true, nextCursor: 'a' } },
      a: { data: [3, 4], meta: { hasMore: true, nextCursor: 'b' } },
      b: { data: [5], meta: { hasMore: false, nextCursor: null } },
    };
    const readPage = vi.fn(async (cursor: string | undefined) => pages[(cursor ?? '') as keyof typeof pages]);

    await expect(readAllPages(readPage)).resolves.toEqual({ data: [1, 2, 3, 4, 5], meta: { hasMore: false, nextCursor: null } });
    expect(readPage.mock.calls.map(([cursor]) => cursor)).toEqual([undefined, 'a', 'b']);
  });

  it('fails instead of cutting the list when a cursor repeats or is missing', async () => {
    await expect(readAllPages(async () => ({ data: [1], meta: { hasMore: true, nextCursor: 'same' } })))
      .rejects.toThrow('no new cursor');
    await expect(readAllPages(async () => ({ data: [1], meta: { hasMore: true, nextCursor: null } })))
      .rejects.toThrow('no new cursor');
  });

  it('fails after too many pages', async () => {
    let page = 0;
    await expect(readAllPages(async () => ({ data: [page], meta: { hasMore: true, nextCursor: String(++page) } })))
      .rejects.toThrow('more than 200 pages');
  });
});
