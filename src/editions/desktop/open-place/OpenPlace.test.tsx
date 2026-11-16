// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { createCanonKeeperQueryClient } from '../../../app/AppProviders';
import { draftChapterId, projectId } from '../../../api/mock/repository/fixtures';
import { staleWords } from '../../../features/manuscript/reader/ReaderStates';
import { ApiStatusError, type Chapter } from '../../../shared/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { paragraphsInOrder } from '../../../shared/text/place';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { OpenPlace, openPlaceWords } from './OpenPlace';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

type Api = ReturnType<typeof createMockApiClient>;

/** The reads the page may do; it writes nothing (AL1.6). */
const reads = new Set(['getChapter', 'getBook', 'getProject']);

/** The page at `address` over the mock, every call of the client counted; the queries of a fresh client unless given. */
function open(address: string, api: Api = createMockApiClient(), client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const calls = Object.entries(api)
    .filter(([, value]) => typeof value === 'function')
    .map(([name]) => [name, vi.spyOn(api as unknown as Record<string, (...args: unknown[]) => unknown>, name)] as const);
  const navigate = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <ToastsProvider><OpenPlace api={api} navigate={navigate} route={parseRoute(`http://localhost${address}`)} /></ToastsProvider>
    </QueryClientProvider>,
  );
  const called = () => calls.filter(([, spy]) => spy.mock.calls.length > 0).map(([name]) => name);
  return { api, navigate, called };
}

function notFound(operationId: string) {
  return new ApiStatusError(404, { type: 'about:blank', status: 404, title: 'Not found', code: 'not_found', requestId: operationId }, new Response(null, { status: 404 }));
}

/** The chapter of the mock, its book and its published revision, as the page reads them. */
async function chapterOfTheMock() {
  const api = createMockApiClient();
  const chapter = await api.getChapter(draftChapterId, 'published');
  const book = await api.getBook(chapter.bookId);
  expect(book.projectId).toBe(projectId);
  return { api: createMockApiClient(), chapter };
}

const address = (query: string) => `/manuscript/open?saga=${projectId}&chapter=${draftChapterId}${query}`;

// specs/2026-11-16-desk-app-links.md AL1.6: the page finds the book of the chapter and the N-th paragraph by its order, and
// replaces its address with the address of the reader; it only reads.
describe('the page a link of the app opens', () => {
  it('opens the reader at the N-th paragraph by its order, by the rule of the panel of the source', async () => {
    const { api, chapter } = await chapterOfTheMock();
    // The paragraphs come in any order of the array: the number counts them by `order`.
    vi.spyOn(api, 'getChapter').mockResolvedValue({ ...chapter, paragraphs: [...chapter.paragraphs].reverse() } as Chapter);
    const { navigate, called } = open(address('&p=3'), api);
    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId, bookId: chapter.bookId, chapterId: chapter.id, mode: 'read', paragraphId: paragraphsInOrder(chapter.paragraphs)[2]!.id,
    }, { replace: true });
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(called().every((name) => reads.has(name))).toBe(true);
    expect(screen.queryByRole('status')?.textContent ?? '').not.toMatch(/ссылк|абзац/u);
  });

  it('opens the chapter from its top without a paragraph, and gives the revision of the link to the reader', async () => {
    const { chapter } = await chapterOfTheMock();
    const plain = open(address(''));
    await waitFor(() => expect(plain.navigate).toHaveBeenCalled());
    expect(plain.navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId, bookId: chapter.bookId, chapterId: chapter.id, mode: 'read',
    }, { replace: true });
    cleanup();
    const revised = open(address('&rev=3'));
    await waitFor(() => expect(revised.navigate).toHaveBeenCalled());
    expect(revised.navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId, bookId: chapter.bookId, chapterId: chapter.id, mode: 'read', expectedRevision: '3',
    }, { replace: true });
  });

  it('opens the chapter without a paragraph past its end and says so in the words of the reader', async () => {
    const { chapter } = await chapterOfTheMock();
    const past = chapter.paragraphs.length + 1;
    const { navigate } = open(address(`&p=${past}&rev=${chapter.revision}`));
    expect(await screen.findByText(staleWords.paragraph)).toBeTruthy();
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId, bookId: chapter.bookId, chapterId: chapter.id, mode: 'read', expectedRevision: String(chapter.revision),
    }, { replace: true });
    cleanup();
    // The chapter changed since the link: the reader says it by its own line; the page adds nothing.
    const changed = open(address(`&p=${past}&rev=${chapter.revision + 1}`));
    await waitFor(() => expect(changed.navigate).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText(staleWords.paragraph)).toBeNull();
  });

  it('takes the author to the shelf of the saga when the chapter is not here, or to the first saga when the saga is gone', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getChapter').mockRejectedValue(notFound('getChapter'));
    const { navigate, called } = open(address('&p=2'), api);
    expect(await screen.findByText(openPlaceWords.noChapter)).toBeTruthy();
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId }, { replace: true });
    expect(called().every((name) => reads.has(name))).toBe(true);
    cleanup();
    const gone = createMockApiClient();
    vi.spyOn(gone, 'getChapter').mockRejectedValue(notFound('getChapter'));
    vi.spyOn(gone, 'getProject').mockRejectedValue(notFound('getProject'));
    const second = open(`/manuscript/open?saga=project-of-another-computer&chapter=${draftChapterId}`, gone);
    expect(await screen.findByText(openPlaceWords.noChapter)).toBeTruthy();
    expect(second.navigate).toHaveBeenCalledWith('/manuscript/books', {}, { replace: true });
  });

  it('takes the author to the shelf when the chapter is of a book of another saga', async () => {
    const { api, chapter } = await chapterOfTheMock();
    const book = await api.getBook(chapter.bookId);
    vi.spyOn(api, 'getBook').mockResolvedValue({ ...book, projectId: 'project-foreign-archive' });
    const { navigate } = open(address(''), api);
    expect(await screen.findByText(openPlaceWords.noChapter)).toBeTruthy();
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId }, { replace: true });
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  // The second link to a chapter within the minutes the app keeps what it read: the page decides by the chapter as it is now —
  // its paragraphs moved, then it is gone —, not by what an earlier link read.
  it('decides by what it read after it opened, not by what an earlier link read', async () => {
    const client = createCanonKeeperQueryClient();
    const { api, chapter } = await chapterOfTheMock();
    const first = open(address('&p=2'), api, client);
    await waitFor(() => expect(first.navigate).toHaveBeenCalled());
    expect(first.navigate.mock.calls[0]![1]).toMatchObject({ paragraphId: paragraphsInOrder(chapter.paragraphs)[1]!.id });
    cleanup();

    // A paragraph was put before all the others: the second is the one that was first.
    const ordered = paragraphsInOrder(chapter.paragraphs);
    const changed = {
      ...chapter, revision: chapter.revision + 1,
      paragraphs: [{ ...ordered[0]!, id: 'paragraph-put-first', order: ordered[0]!.order - 1, text: 'Новый первый абзац' }, ...ordered],
    } as Chapter;
    vi.spyOn(api, 'getChapter').mockResolvedValue(changed);
    const second = open(address(`&p=2&rev=${changed.revision}`), api, client);
    await waitFor(() => expect(second.navigate).toHaveBeenCalled());
    expect(second.navigate).toHaveBeenCalledTimes(1);
    expect(second.navigate.mock.calls[0]![1]).toMatchObject({ paragraphId: ordered[0]!.id, expectedRevision: String(changed.revision) });
    cleanup();

    // Then the chapter is gone: the shelf and the line, not the reader of a chapter that is not here.
    vi.spyOn(api, 'getChapter').mockRejectedValue(notFound('getChapter'));
    const third = open(address('&p=2'), api, client);
    expect(await screen.findByText(openPlaceWords.noChapter)).toBeTruthy();
    expect(third.navigate).toHaveBeenCalledTimes(1);
    expect(third.navigate).toHaveBeenCalledWith('/manuscript/books', { projectId }, { replace: true });
  });

  it('says a link with a mistake and reads nothing — the one the shell refused and the one the page sees itself', async () => {
    for (const query of [
      '/manuscript/open?refused=1',
      address('&p=015'),
      address('&p=0'),
      address('&p=100000'),
      address('&p=x'),
      address('&rev=0'),
      address('&rev=9007199254740992'),
      address('&rev=1.5'),
      `/manuscript/open?saga=${projectId}`,
      `/manuscript/open?chapter=${draftChapterId}`,
      '/manuscript/open',
    ]) {
      const { navigate, called } = open(query);
      expect(await screen.findByText(openPlaceWords.refused)).toBeTruthy();
      expect(navigate).toHaveBeenCalledWith('/manuscript/books', {}, { replace: true });
      expect(called(), query).toEqual([]);
      cleanup();
    }
  });
});
