import { queryOptions, useQuery } from '@tanstack/react-query';
import type {
  Chapter,
  ImportQueue,
  ManuscriptApiClient,
  ManuscriptReaderWorkspace,
  ManuscriptWorkspaceParams,
} from '../../../entities/manuscript/api';
import {
  manuscriptQueryKeys,
  readAllPages,
  textSourceView,
  selectBook,
  selectProject,
  selectPublishedChapter,
} from '../../../entities/manuscript/api';
export * from './mutations';

type ManuscriptJobClient = Pick<ManuscriptApiClient, 'getJob'>;
type RecentlyDeletedClient = Pick<ManuscriptApiClient, 'listRecentlyDeleted'>;
type ManuscriptWorkspaceClient = Pick<ManuscriptApiClient, 'getChapter' | 'getProject' | 'getBook' | 'listBooks' | 'listChapters' | 'listProjects'>;
type ImportConstraintsClient = Pick<ManuscriptApiClient, 'getImportConstraints'>;
type ImportQueuesClient = Pick<ManuscriptApiClient, 'listImportQueues'>;
type ImportQueueClient = Pick<ManuscriptApiClient, 'getImportQueue'>;

interface ManuscriptQueryMeta<T> extends Record<string, unknown> {
  manuscriptApi: T;
}

function manuscriptApiMeta<T>(api: T): ManuscriptQueryMeta<T> {
  return { manuscriptApi: api };
}

function manuscriptApiFromMeta<T>(meta: unknown) {
  const queryMeta = meta as Partial<ManuscriptQueryMeta<T>> | undefined;
  if (!queryMeta?.manuscriptApi) {
    throw new Error('Manuscript query is missing CanonKeeper API client metadata.');
  }
  return queryMeta.manuscriptApi;
}

const jobReconciliationIntervalMs = 2_000;

/**
 * The one query of a Job: every place that reads it — the page that started it, the shell that says its end — sees the
 * same answer at the same moment (specs/2026-11-12-ux12-shell.md SH2.2). `followWhileActive` polls the Job until it ends.
 */
export function manuscriptJobQuery(api: ManuscriptJobClient, jobId: string, followWhileActive = false) {
  return queryOptions({
    queryKey: manuscriptQueryKeys.job(jobId),
    queryFn: ({ meta }) => manuscriptApiFromMeta<ManuscriptJobClient>(meta).getJob(jobId),
    meta: manuscriptApiMeta(api),
    refetchInterval: (query) => followWhileActive
      && ['queued', 'running', 'canceling'].includes(query.state.data?.status ?? 'queued')
      ? jobReconciliationIntervalMs
      : false,
  });
}

/** `followWhileActive` polls the Job until it ends, for a Job this page started itself. */
export function useManuscriptJobQuery(api: ManuscriptJobClient, jobId: string, enabled = true, followWhileActive = false) {
  return useQuery({ ...manuscriptJobQuery(api, jobId, followWhileActive), enabled: enabled && jobId.length > 0 });
}

const importQueueReconciliationIntervalMs = 2_000;

/** The volumes the server still works on; one that waits for the author or was skipped asks the server nothing. */
const workingVolumeStatuses: ReadonlySet<ImportQueue['items'][number]['processingStatus']> = new Set([
  'uploading', 'ready', 'queued', 'importing', 'indexing', 'retrying', 'canceling',
]);

/**
 * A queue is read again while the server works on it. A running queue whose volumes are all read, waiting for the author
 * at their places (specs/2026-11-11-ux7-upload.md UP3.2) or skipped is still: nothing changes until the author acts.
 */
function importQueueNeedsPolling(queue: ImportQueue | undefined) {
  if (queue?.status === 'uploading') return true;
  return queue?.status === 'running' && queue.items.some((item) => workingVolumeStatuses.has(item.processingStatus));
}

/**
 * The formats and the size limit the server accepts, read where the author can load books: every file is checked
 * against them before any transfer (UC3.1).
 */
export function useImportConstraintsQuery(api: ImportConstraintsClient, projectId: string, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.importConstraints(projectId),
    queryFn: ({ meta }) => manuscriptApiFromMeta<ImportConstraintsClient>(meta).getImportConstraints(projectId),
    meta: manuscriptApiMeta(api),
    enabled: enabled && projectId.length > 0,
  });
}

export function useImportQueuesQuery(api: ImportQueuesClient, projectId: string) {
  return useQuery({
    queryKey: manuscriptQueryKeys.importQueues(projectId),
    queryFn: ({ meta, signal }) => manuscriptApiFromMeta<ImportQueuesClient>(meta).listImportQueues(projectId, { signal }),
    meta: manuscriptApiMeta(api),
    enabled: projectId.length > 0,
    refetchInterval: (query) => (query.state.data?.data ?? []).some(importQueueNeedsPolling)
      ? importQueueReconciliationIntervalMs
      : false,
  });
}

type BookChaptersClient = Pick<ManuscriptApiClient, 'listChapters'>;

/** The query key of every chapter of a book, as «Как разобралась книга» reads them (specs/2026-11-11-ux7-upload.md UP4.2). */
export const bookChaptersQueryKey = (bookId: string) => [...manuscriptQueryKeys.all, 'book-chapters', bookId] as const;

export function useBookChaptersQuery(api: BookChaptersClient, bookId: string) {
  return useQuery({
    queryKey: bookChaptersQueryKey(bookId),
    queryFn: ({ meta }) => readAllPages((cursor) => manuscriptApiFromMeta<BookChaptersClient>(meta).listChapters(bookId, { cursor })),
    meta: manuscriptApiMeta(api),
    enabled: bookId.length > 0,
  });
}

export function useImportQueueQuery(api: ImportQueueClient, projectId: string, queueId: string) {
  return useQuery({
    queryKey: manuscriptQueryKeys.importQueue(projectId, queueId),
    queryFn: ({ meta, signal }) => manuscriptApiFromMeta<ImportQueueClient>(meta).getImportQueue(projectId, queueId, { signal }),
    meta: manuscriptApiMeta(api),
    enabled: projectId.length > 0 && queueId.length > 0,
  });
}

/** The book open in the reader, with every saga, book and chapter of its lists (specs/2026-11-07-chapter-list-paging.md, P2.1). */
async function loadReaderWorkspace(api: ManuscriptWorkspaceClient, params: ManuscriptWorkspaceParams): Promise<ManuscriptReaderWorkspace> {
  const projectList = await readAllPages((cursor) => api.listProjects({ cursor }));
  const projectSummary = selectProject(projectList.data, params.projectId);
  if (!projectSummary) {
    return { book: null, chapter: null, chapters: [], project: null, projects: projectList.data };
  }

  const [project, bookList] = await Promise.all([
    api.getProject(projectSummary.id),
    readAllPages((cursor) => api.listBooks(projectSummary.id, { cursor })),
  ]);
  const book = selectBook(bookList.data, params.bookId);
  if (!book) {
    return { book: null, chapter: null, chapters: [], project, projects: projectList.data };
  }

  let chapterList: Awaited<ReturnType<ManuscriptWorkspaceClient['listChapters']>>;
  try {
    chapterList = await readAllPages((cursor) => api.listChapters(book.id, { cursor }));
  } catch (loadError) {
    return { book, chapter: null, chapters: [], loadError, project, projects: projectList.data };
  }
  const chapterSummary = selectPublishedChapter(chapterList.data, params.chapterId);
  if (!chapterSummary) {
    return { book, chapter: null, chapters: chapterList.data, project, projects: projectList.data };
  }

  let chapter: Chapter;
  try {
    chapter = await api.getChapter(chapterSummary.id, textSourceView);
  } catch (loadError) {
    return { book, chapter: null, chapters: chapterList.data, loadError, project, projects: projectList.data };
  }

  return {
    book,
    chapter,
    chapters: chapterList.data,
    project,
    projects: projectList.data,
  };
}

export function useManuscriptReaderWorkspaceQuery(api: ManuscriptWorkspaceClient, params: ManuscriptWorkspaceParams, options: { enabled?: boolean } = {}) {
  const { enabled = true } = options;
  return useQuery({
    queryKey: manuscriptQueryKeys.readerWorkspace(params),
    queryFn: ({ meta }) => loadReaderWorkspace(manuscriptApiFromMeta<ManuscriptWorkspaceClient>(meta), params),
    meta: manuscriptApiMeta(api),
    enabled,
  });
}

/**
 * «Недавно удалённые» of the author over all his sagas (specs/2026-11-11-ux7-trash.md TB1.1): one read, read again each
 * time the window opens.
 */
export function useRecentlyDeletedQuery(api: RecentlyDeletedClient, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.recentlyDeleted(),
    queryFn: ({ meta }) => manuscriptApiFromMeta<RecentlyDeletedClient>(meta).listRecentlyDeleted(),
    meta: manuscriptApiMeta(api),
    enabled,
  });
}
