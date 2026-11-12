import type { AppRoute } from '../../../shared/navigation/app-route';

/** The tabs of «Содержание» (specs/2026-11-10-ux7-reader.md RD1.7); the open one is in the address. */
export const readerPanels = ['chapters', 'notes', 'drawings', 'found'] as const;
export type ReaderPanel = typeof readerPanels[number];

/**
 * The tab of «Содержание» the address opens (`contents`; `panel` is the panel of the lightbox of a drawing); an old link
 * with `chapters=open` opens «Главы».
 */
export function readerPanel(query: URLSearchParams): ReaderPanel | null {
  const panel = query.get('contents');
  const named = readerPanels.find((item) => item === panel);
  if (named) return named;
  return query.get('chapters') === 'open' ? 'chapters' : null;
}

/** What the address of the reader keeps from a step to the next; a parameter of an old link outside it is dropped. */
const kept = ['projectId', 'bookId', 'mode', 'chapterId', 'paragraphId', 'expectedRevision', 'focus', 'contents', 'note', 'view', 'find', 'illustrated'] as const;

/**
 * The address of the reader after a change: the place, the look and the open panels stay, the rest is dropped. `chapters`
 * of an old link becomes `contents`.
 */
export function readerQuery(route: Pick<AppRoute, 'query'>, overrides: Record<string, string | undefined> = {}) {
  const query: Record<string, string | undefined> = {};
  for (const key of kept) query[key] = route.query.get(key) ?? undefined;
  query.mode = query.mode ?? 'read';
  query.contents = readerPanel(route.query) ?? undefined;
  const next = { ...query, ...overrides };
  // A note is open only beside the text, in «Заметки» of «Содержание» (specs/2026-11-11-ux7-notes.md NT2.3).
  if (next.contents !== 'notes') next.note = undefined;
  return next;
}

/** The step to another chapter: the look, the panel and the search stay; the place in the old chapter goes. */
export function chapterQuery(route: Pick<AppRoute, 'query'>, chapter: { projectId?: string; bookId?: string; chapterId: string; paragraphId?: string }) {
  return readerQuery(route, {
    projectId: chapter.projectId, bookId: chapter.bookId, chapterId: chapter.chapterId,
    // The note open beside the text is a note of the chapter left (specs/2026-11-11-ux7-notes.md NT2.3).
    paragraphId: chapter.paragraphId, expectedRevision: undefined, view: undefined, note: undefined,
  });
}
