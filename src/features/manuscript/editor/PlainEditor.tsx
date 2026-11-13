import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import {
  chapterEditQueryKeys, useBegunChapterQuery, useEditedDraftQuery, useManuscriptShelfQuery,
  type ChapterEditApiClient, type EditedDraft, type ManuscriptApiClient,
} from '../../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useBookChaptersQuery } from '../api';
import { EditorScreen } from './EditorScreen';
import { editorPath } from './editor-address';
import { editorWords } from './editor-words';
import styles from './PlainEditor.module.css';

/** What the editor asks of the API: the drafts, and the saga, its books and their chapters as the shelf reads them. */
export type PlainEditorApi = ChapterEditApiClient & Pick<ManuscriptApiClient, 'getProject' | 'listBooks' | 'listChapters' | 'listProjectJobs' | 'listProjects'>;

export interface PlainEditorProps {
  api: PlainEditorApi;
  navigate: RouteNavigate;
  route: AppRoute;
  /** The author: what he typed and did not save is kept in this browser under his name. */
  author: string;
  saveDelayMs?: number;
}

/**
 * «Править главу» and «Новая глава» of the desktop app without the helper (specs/2026-11-13-desk-editor.md ED1, ED2) at
 * its address (`editorPath`): `draftId` — a draft, the edit of a chapter of the book or a new chapter written down;
 * `bookId` without it — a new chapter of the book, the one begun and not added if there is one, else an empty one, written
 * down by its first save. The screen stays the same component when that first save gives it its address.
 */
export function PlainEditor({ api, author, navigate, route, saveDelayMs }: PlainEditorProps) {
  const queryClient = useQueryClient();
  const projectId = route.query.get('projectId') ?? '';
  const draftId = route.query.get('draftId') ?? '';
  const askedBook = route.query.get('bookId') ?? '';
  const [createdHere, setCreatedHere] = useState<string | null>(null);

  const draftQuery = useEditedDraftQuery(api, projectId, draftId);
  const begun = useBegunChapterQuery(api, projectId, askedBook, !draftId);
  const shelf = useManuscriptShelfQuery(api, { projectId });
  const draft = draftId ? draftQuery.data ?? null : null;
  const bookId = draft ? draft.edits?.bookId ?? draft.place.bookId ?? '' : askedBook;
  const chaptersQuery = useBookChaptersQuery(api, bookId);
  const book = shelf.data?.books.find((item) => item.id === bookId) ?? null;
  const chapters = (chaptersQuery.data?.data ?? []).filter((item) => item.status === 'published' || item.status === 'stale')
    .sort((left, right) => left.order - right.order);

  // The chapter of the book begun and not added opens again: no second one is written down (ED2.2).
  const begunDraft = begun.data;
  useEffect(() => {
    if (draftId || !begunDraft) return;
    queryClient.setQueryData(chapterEditQueryKeys.draft(projectId, begunDraft.id), begunDraft);
    navigate(editorPath, { projectId, draftId: begunDraft.id }, { replace: true });
  }, [begunDraft, draftId, navigate, projectId, queryClient]);
  // A draft already in the book is read there.
  const addedTo = draft?.added?.chapterId;
  useEffect(() => {
    if (addedTo) navigate('/manuscript/books', { projectId, bookId, chapterId: addedTo, mode: 'read' }, { replace: true });
  }, [addedTo, bookId, navigate, projectId]);

  const toBooks = { label: 'К книгам', onClick: () => navigate('/manuscript/books', { projectId }) };
  const failure = (draftId ? draftQuery.error : begun.error) ?? shelf.error ?? chaptersQuery.error;
  if (failure) {
    return (
      <section aria-label={editorWords.openFailed} className={styles.editor}>
        <ErrorBlock {...loadFailureWords(failure)} action={toBooks} title={editorWords.openFailed} />
      </section>
    );
  }
  const newOne = !draftId && begun.isSuccess && !begun.data;
  const known = shelf.isSuccess && (Boolean(draft) || newOne) && chaptersQuery.isSuccess && !addedTo;
  if (known && !book) {
    return (
      <section aria-label={editorWords.openFailed} className={styles.editor}>
        <ErrorBlock action={toBooks} cause="Её нет: она удалена или ссылка устарела." title={editorWords.openFailed} />
      </section>
    );
  }
  if (!known || !book) {
    return <section aria-busy="true" className={styles.editor}><SkeletonArea count={6} shape="lines" /></section>;
  }
  // The new chapter keeps its screen when its first save gives it an address: what was typed meanwhile stays.
  const key = draftId && draftId !== createdHere ? draftId : `new:${bookId}`;
  return (
    <EditorScreen api={api} author={author} book={book} canEdit={shelf.data.canEditManuscript} chapters={chapters} draft={draft} key={key}
      navigate={navigate} onCreated={(made: EditedDraft) => {
        setCreatedHere(made.id);
        navigate(editorPath, { projectId, draftId: made.id }, { replace: true });
      }} projectId={projectId} {...(saveDelayMs === undefined ? {} : { saveDelayMs })} />
  );
}
