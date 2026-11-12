import { useQueries, useQuery } from '@tanstack/react-query';
import type { ChapterListItem, ManuscriptApiClient, ReaderChapter } from '../../../entities/manuscript/api';
import { manuscriptQueryKeys, readAllPages, textSourceView } from '../../../entities/manuscript/api';
import { findInChapter, findTerms, minFindLength, type FoundPlace } from './reader-find';

export type BookFindClient = Pick<ManuscriptApiClient, 'searchProject' | 'getChapter'>;

type FoundChapter = { chapterId: string; paragraphId: string | null };

/** The client travels in query meta: it is a dependency of the query function, not part of the cached key. */
function clientOf(meta: unknown) {
  const client = (meta as { readerFindApi?: BookFindClient } | undefined)?.readerFindApi;
  if (!client) throw new Error('The search of the reader is missing its API.');
  return client;
}

export type BookFind =
  | { state: 'idle' }
  | { state: 'searching' }
  | { state: 'failed'; retry: () => void }
  | { state: 'ready'; places: FoundPlace[]; texts: ReadonlyMap<string, ReaderChapter>; complete: boolean };

/**
 * «Найти» in the open book (specs/2026-11-10-ux7-reader.md UC2): the search of the server names the chapters of the book
 * that hold the words, by their forms; their text is read once (the cache keeps it) and the page marks every place in
 * reading order — the order of the chapters of the book, then of the paragraphs. A chapter the server named whose words
 * the page cannot mark keeps the paragraph the server found.
 */
export function useBookFind({ api, bookId, chapter, chapters, projectId, query }: {
  api: BookFindClient;
  projectId: string;
  bookId: string;
  /** The chapter on the screen: its text is already here. */
  chapter: ReaderChapter | null;
  /** The chapters of the book in reading order. */
  chapters: readonly ChapterListItem[];
  query: string;
}): BookFind {
  const words = query.trim();
  const enabled = words.length >= minFindLength && Boolean(projectId && bookId);
  const found = useQuery({
    queryKey: [...manuscriptQueryKeys.all, 'reader-find', projectId, bookId, words],
    queryFn: async ({ meta, signal }) => {
      const result = await readAllPages((cursor) => clientOf(meta).searchProject(projectId, words, textSourceView, 'all', {
        filters: { bookId }, limit: 50, ...(cursor ? { cursor } : {}), signal,
      }));
      const seen = new Map<string, FoundChapter>();
      for (const item of result.data) {
        const anchor = item.locator.sourceAnchor;
        if (!seen.has(item.locator.chapterId)) {
          seen.set(item.locator.chapterId, { chapterId: item.locator.chapterId, paragraphId: anchor.kind === 'paragraph' ? anchor.locator.paragraphId ?? null : null });
        }
      }
      return [...seen.values()];
    },
    enabled,
    meta: { readerFindApi: api },
    staleTime: 60_000,
  });
  const order = new Map(chapters.map((item, index) => [item.id, index]));
  const named = (found.data ?? []).filter((item) => order.has(item.chapterId))
    .sort((left, right) => order.get(left.chapterId)! - order.get(right.chapterId)!);
  // The text of the chapter on the screen is here already; the others are read once.
  const others = named.filter((item) => item.chapterId !== chapter?.id);
  const texts = useQueries({
    queries: others.map((item) => ({
      queryKey: manuscriptQueryKeys.sourceChapter(item.chapterId),
      queryFn: ({ meta }: { meta?: Record<string, unknown> }) => clientOf(meta).getChapter(item.chapterId, textSourceView),
      meta: { readerFindApi: api },
      staleTime: 5 * 60_000,
    })),
  });
  if (!enabled) return { state: 'idle' };
  if (found.isError) return { state: 'failed', retry: () => void found.refetch() };
  if (!found.data) return { state: 'searching' };
  const terms = findTerms(words);
  const loaded = new Map<string, ReaderChapter>();
  const places: FoundPlace[] = [];
  named.forEach((item) => {
    const text = item.chapterId === chapter?.id ? chapter : texts[others.indexOf(item)]?.data;
    if (!text || text.contentVariant !== textSourceView) return;
    loaded.set(item.chapterId, text);
    const inChapter = findInChapter(item.chapterId, text.paragraphs.filter((paragraph) => paragraph.kind !== 'heading')
      .map((paragraph) => ({ id: paragraph.id, text: paragraph.text })), terms)
      .map((place) => ({ ...place, paragraph: text.paragraphs.findIndex((paragraph) => paragraph.id === place.paragraphId) + 1 }));
    if (inChapter.length) {
      places.push(...inChapter);
      return;
    }
    const position = item.paragraphId ? text.paragraphs.findIndex((paragraph) => paragraph.id === item.paragraphId) : -1;
    const paragraph = text.paragraphs[position];
    if (paragraph && paragraph.kind !== 'heading') {
      places.push({ chapterId: item.chapterId, paragraphId: paragraph.id, paragraph: position + 1, start: 0, end: paragraph.text.length });
    }
  });
  return { state: 'ready', places, texts: loaded, complete: loaded.size === named.length };
}
