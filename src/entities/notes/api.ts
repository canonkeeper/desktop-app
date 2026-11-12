import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import type { CanonKeeperApiClient, OperationResponse } from '../../shared/api';

export type NotesApiClient = Pick<CanonKeeperApiClient,
  'listNotes' | 'createPersonalNote' | 'getPersonalNote' | 'updatePersonalNote'
  | 'deletePersonalNote' | 'resolvePersonalNoteSource'>;
export type { NotesListEntry, PersonalNote, PersonalNoteSource } from '../../shared/api';
export { createIdempotencyKey } from '../../shared/api';
export type NoteResolvedSource = NonNullable<OperationResponse<'resolvePersonalNoteSource', '200'>['source']>;
export type NotesFilters = Pick<NonNullable<Parameters<CanonKeeperApiClient['listNotes']>[0]>, 'query' | 'source' | 'projectId' | 'bookId'>;

/** The client travels in query meta: it is a dependency of the query function, not part of the cached key. */
export function notesMeta(api: NotesApiClient) {
  return { outageScope: 'local', notesApi: api } as const;
}

export function notesApiFromMeta(meta: unknown) {
  const value = meta as { notesApi?: NotesApiClient } | undefined;
  if (!value?.notesApi) throw new Error('Notes query is missing API metadata.');
  return value.notesApi;
}

export const noteQueryKeys = {
  all: ['canonkeeper', 'notes'] as const,
  list: (accountId: string) => [...noteQueryKeys.all, accountId, 'list'] as const,
  note: (accountId: string, id: string) => [...noteQueryKeys.all, accountId, 'note', id] as const,
  source: (accountId: string, id: string) => [...noteQueryKeys.all, accountId, 'source', id] as const,
};

export function useNotesList(api: NotesApiClient, accountId: string, enabled: boolean, filters: NotesFilters = {}) {
  return useInfiniteQuery({
    queryKey: [...noteQueryKeys.list(accountId), filters],
    queryFn: ({ meta, pageParam, signal }) => notesApiFromMeta(meta).listNotes({ ...filters, cursor: pageParam, limit: 30, signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.meta.hasMore ? page.meta.nextCursor ?? undefined : undefined,
    enabled,
    meta: notesMeta(api),
  });
}

export function usePersonalNote(api: NotesApiClient, accountId: string, noteId: string) {
  return useQuery({
    queryKey: noteQueryKeys.note(accountId, noteId),
    queryFn: ({ meta, signal }) => notesApiFromMeta(meta).getPersonalNote(noteId, { signal }),
    enabled: Boolean(noteId),
    meta: notesMeta(api),
  });
}

/**
 * Where the place of a note is now (specs/2026-11-11-ux7-notes.md NT1.1, NT2.4): the place of its chapter in the words of
 * the server, or that the book no longer has it. A note of the book reads it when it opens; a note of a chat reads it by
 * the press of its link, as that reads the conversation.
 */
export function useNoteSource(api: NotesApiClient, accountId: string, noteId: string, enabled: boolean) {
  return useQuery({
    queryKey: noteQueryKeys.source(accountId, noteId),
    queryFn: ({ meta, signal }) => notesApiFromMeta(meta).resolvePersonalNoteSource(noteId, { signal }),
    enabled: enabled && Boolean(noteId),
    meta: notesMeta(api),
    retry: false,
  });
}

/**
 * The line a note is named by (NT1.3): an older note's own title, the first line of the thought, or — without a thought —
 * the beginning of the cited words in quotation marks.
 */
export function noteLine(note: { title: string; thought: string; quote?: string | undefined }) {
  const firstLine = (text: string) => text.split('\n').map((line) => line.trim()).find(Boolean) ?? '';
  const quote = firstLine(note.quote ?? '');
  return note.title.trim() || firstLine(note.thought) || (quote ? `«${quote}»` : '');
}
