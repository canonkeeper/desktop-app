import { useQuery } from '@tanstack/react-query';
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
type DeletedProjectsClient = Pick<ManuscriptApiClient, 'listProjects'>;
type DeletedBooksClient = Pick<ManuscriptApiClient, 'listBooks'>;
type DeletedChaptersClient = Pick<ManuscriptApiClient, 'listChapters'>;
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

/** `followWhileActive` polls the Job until it ends, for a Job this page started itself. */
export function useManuscriptJobQuery(api: ManuscriptJobClient, jobId: string, enabled = true, followWhileActive = false) {
  return useQuery({
    queryKey: manuscriptQueryKeys.job(jobId),
    queryFn: ({ meta }) => manuscriptApiFromMeta<ManuscriptJobClient>(meta).getJob(jobId),
    meta: manuscriptApiMeta(api),
    enabled: enabled && jobId.length > 0,
    refetchInterval: (query) => followWhileActive
      && ['queued', 'running', 'canceling'].includes(query.state.data?.status ?? 'queued')
      ? jobReconciliationIntervalMs
      : false,
  });
}

const importQueueReconciliationIntervalMs = 2_000;

function importQueueNeedsPolling(queue: ImportQueue | undefined) {
  return queue?.status === 'uploading' || queue?.status === 'running';
}

/**
 * The formats, size limit and next book number the server accepts, read where the author can
 * load books: every file is checked against them before any transfer (UC3.1, UC3.2).
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

export function useDeletedProjectsQuery(api: DeletedProjectsClient, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.deletedProjects(),
    queryFn: ({ meta }) => readAllPages((cursor) => manuscriptApiFromMeta<DeletedProjectsClient>(meta)
      .listProjects({ state: 'deleted', cursor })),
    meta: manuscriptApiMeta(api),
    enabled,
  });
}

export function useDeletedBooksQuery(api: DeletedBooksClient, projectId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.deletedBooks(projectId),
    queryFn: ({ meta }) => readAllPages((cursor) => manuscriptApiFromMeta<DeletedBooksClient>(meta)
      .listBooks(projectId!, { state: 'deleted', cursor })),
    meta: manuscriptApiMeta(api),
    enabled: enabled && Boolean(projectId),
  });
}

export function useDeletedChaptersQuery(api: DeletedChaptersClient, bookId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.deletedChapters(bookId),
    queryFn: ({ meta }) => readAllPages((cursor) => manuscriptApiFromMeta<DeletedChaptersClient>(meta)
      .listChapters(bookId!, { state: 'deleted', cursor })),
    meta: manuscriptApiMeta(api),
    enabled: enabled && Boolean(bookId),
  });
}
