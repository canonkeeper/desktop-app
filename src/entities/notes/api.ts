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
