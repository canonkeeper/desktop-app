import { useQueries } from '@tanstack/react-query';
import { useEffect } from 'react';
import { noteLine, noteQueryKeys, notesApiFromMeta, notesMeta, useNotesList, type NotesApiClient } from '../../../entities/notes/api';
import { useCurrentUserQuery, type CurrentUserClient } from '../../../entities/session/api';
import type { ContentsNote } from './ReaderContents';

export type ChapterNotesClient = NotesApiClient & CurrentUserClient;

/** Pages of the notes of a book the reader follows: three hundred notes at the default page. */
const maxPages = 10;

/**
 * The notes of the author to the open chapter (specs/2026-11-10-ux7-reader.md RD1.7, RD3.3): the notes of the book whose
 * chapter carries this title, then each of them by its source — the paragraph it stands at. They are his own («Только для
 * вас»): the list of the server answers the account.
 */
export function useChapterNotes(api: ChapterNotesClient, { bookId, chapterId, chapterTitle, projectId }: {
  projectId: string;
  bookId: string;
  chapterId: string;
  chapterTitle: string;
}): { state: 'loading' | 'failed' | 'ready'; items: ContentsNote[] } {
  const accountId = useCurrentUserQuery(api).data?.id ?? '';
  const enabled = Boolean(accountId && projectId && bookId && chapterId);
  const list = useNotesList(api, accountId, enabled, { projectId, bookId, source: 'chapter' });
  const pages = list.data?.pages.length ?? 0;
  useEffect(() => {
    if (list.hasNextPage && !list.isFetchingNextPage && pages < maxPages) void list.fetchNextPage();
  }, [list, pages]);
  const entries = (list.data?.pages.flatMap((page) => page.data) ?? []).filter((entry) => entry.chapterTitle === chapterTitle);
  const notes = useQueries({
    queries: entries.map((entry) => ({
      queryKey: noteQueryKeys.note(accountId, entry.id),
      queryFn: ({ meta, signal }: { meta?: Record<string, unknown>; signal: AbortSignal }) => notesApiFromMeta(meta).getPersonalNote(entry.id, { signal }),
      meta: notesMeta(api),
      enabled,
    })),
  });
  if (!enabled || list.isPending) return { state: 'loading', items: [] };
  if (list.isError) return { state: 'failed', items: [] };
  const items: ContentsNote[] = [];
  entries.forEach((entry, index) => {
    const source = notes[index]?.data?.source;
    if (source?.kind !== 'chapter' || source.locator.chapterId !== chapterId || !source.locator.paragraphId) return;
    // A note of a selection may have no thought: its line is then the beginning of its cited words (specs/2026-11-11-ux7-notes.md NT1.3).
    items.push({ id: entry.id, paragraphId: source.locator.paragraphId, title: noteLine({ title: entry.title, thought: entry.preview, quote: entry.quote }).slice(0, 80), preview: entry.title ? entry.preview : '' });
  });
  return { state: notes.some((note) => note.isPending) ? 'loading' : 'ready', items };
}
