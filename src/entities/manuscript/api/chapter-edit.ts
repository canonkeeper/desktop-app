import { replaceEqualDeep, useQuery } from '@tanstack/react-query';
import { readAllPages, type CanonKeeperApiClient, type OperationRequest, type Schema } from '../../../shared/api';

/**
 * The operations of the editor of a chapter without the helper (specs/2026-11-13-desk-editor.md): the chapter of the book
 * edited and replaced, a new chapter written and added — all of them of the desktop contract.
 */
export type ChapterEditApiClient = Pick<CanonKeeperApiClient,
  | 'getChapterDraft' | 'updateChapterDraft' | 'replaceChapterFromDraft' | 'revertChapterFromDraft' | 'reopenChapterFromBook'
  | 'listChapterDraftVersions' | 'getChapterDraftVersion' | 'takeChapterDraftVersion' | 'createChapterDraft' | 'addChapterDraftToBook'
  | 'deleteChapterDraft' | 'listChapterDrafts'>;
export type EditedDraft = Schema<'ChapterDraft'>;
export type EditedDraftChange = Omit<OperationRequest<'updateChapterDraft'>, 'expectedRevision'>;
export type EditedDraftVersionSummary = Schema<'ChapterDraftVersionSummary'>;
export type EditedDraftVersion = Schema<'ChapterDraftVersion'>;
export type EditedDraftVersionList = Schema<'ChapterDraftVersionList'>;

/** The drafts live under the product root, so an identity change clears them too; the keys are those of «Письмо». */
export const chapterEditQueryKeys = {
  project: (projectId: string) => ['canonkeeper', 'chapter-drafts', projectId] as const,
  draft: (projectId: string, draftId: string) => [...chapterEditQueryKeys.project(projectId), 'draft', draftId] as const,
  versions: (projectId: string, draftId: string) => [...chapterEditQueryKeys.draft(projectId, draftId), 'of', 'versions'] as const,
  version: (projectId: string, draftId: string, number: number) => [...chapterEditQueryKeys.draft(projectId, draftId), 'of', 'version', number] as const,
  /** The chapter of a book begun in the editor and not added yet (ED2.2). */
  begun: (projectId: string, bookId: string) => [...chapterEditQueryKeys.project(projectId), 'begun', bookId] as const,
};

function editApi(meta: unknown) {
  const api = (meta as { editApi?: ChapterEditApiClient } | undefined)?.editApi;
  if (!api) throw new Error('Chapter edit query is missing API metadata.');
  return api;
}

/** One draft; a read that left before a save does not bring an older revision back — a revision only grows. */
export function useEditedDraftQuery(api: ChapterEditApiClient, projectId: string, draftId: string) {
  return useQuery({
    queryKey: chapterEditQueryKeys.draft(projectId, draftId),
    queryFn: ({ meta, signal }) => editApi(meta).getChapterDraft(projectId, draftId, { signal }),
    meta: { editApi: api },
    enabled: projectId.length > 0 && draftId.length > 0,
    structuralSharing: (old: unknown, next: unknown) => {
      const [before, after] = [old as EditedDraft | undefined, next as EditedDraft];
      return before && after.revision < before.revision ? before : replaceEqualDeep(before, after);
    },
  });
}

/** The versions of the text, the latest first (WA7.5), read while «Версии текста» is open. */
export function useEditedDraftVersionsQuery(api: ChapterEditApiClient, projectId: string, draftId: string, enabled: boolean) {
  return useQuery({
    queryKey: chapterEditQueryKeys.versions(projectId, draftId),
    queryFn: ({ meta, signal }) => editApi(meta).listChapterDraftVersions(projectId, draftId, { signal }),
    meta: { editApi: api },
    enabled: enabled && projectId.length > 0 && draftId.length > 0,
    staleTime: 0,
  });
}

/** A version of the text whole: it never changes, so it is read once by its number. */
export function useEditedDraftVersionQuery(api: ChapterEditApiClient, projectId: string, draftId: string, number: number | null) {
  return useQuery({
    queryKey: chapterEditQueryKeys.version(projectId, draftId, number ?? 0),
    queryFn: ({ meta, signal }) => editApi(meta).getChapterDraftVersion(projectId, draftId, number!, { signal }),
    meta: { editApi: api },
    enabled: number !== null && projectId.length > 0 && draftId.length > 0,
    staleTime: Infinity,
  });
}

/**
 * The new chapter of a book begun and not added (ED2.2): «Новая глава» opens it again instead of a second one. A row of the
 * list has no place, so a draft that edits no chapter and is not in the book is read for its book — the newest first.
 */
export async function begunChapterOf(api: Pick<ChapterEditApiClient, 'listChapterDrafts' | 'getChapterDraft'>, projectId: string, bookId: string) {
  const drafts = await readAllPages((cursor) => api.listChapterDrafts(projectId, { cursor }));
  const candidates = drafts.data.filter((draft) => draft.stage === 'text' && !draft.edits && !draft.attachment)
    .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt));
  for (const candidate of candidates) {
    const draft = await api.getChapterDraft(projectId, candidate.id);
    if (draft.place.bookId === bookId && !draft.added && !draft.edits && !draft.original) return draft;
  }
  return null;
}

export function useBegunChapterQuery(api: ChapterEditApiClient, projectId: string, bookId: string, enabled: boolean) {
  return useQuery({
    queryKey: chapterEditQueryKeys.begun(projectId, bookId),
    queryFn: ({ meta }) => begunChapterOf(editApi(meta), projectId, bookId),
    meta: { editApi: api },
    enabled: enabled && projectId.length > 0 && bookId.length > 0,
    staleTime: 0,
    gcTime: 0,
  });
}
