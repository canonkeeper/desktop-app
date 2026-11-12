import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import { createIdempotencyKey, readAllPages } from '../../../shared/api';
import type {
  Book,
  BookExportFormat,
  CanonKeeperApiClient,
  Chapter,
  DownloadedFile,
  ImportQueue,
  ImportQueueItem,
  Job,
  Project,
  ProjectMembership,
  ReaderLocator,
  Schema,
  SearchScope,
  SourceView,
  UploadProgress,
} from '../../../shared/api';

export { createIdempotencyKey, readAllPages };
export type { Book, BookExportFormat, Chapter, DownloadedFile, ImportQueue, ImportQueueItem, Project, ProjectMembership, ReaderLocator, SearchScope, SourceView, UploadProgress };
export type ReaderSourceClient = Pick<CanonKeeperApiClient, 'getChapter'>;

export type ManuscriptApiClient = Pick<
  CanonKeeperApiClient,
  | 'cancelJob'
  | 'cancelImportQueue'
  | 'createBook'
  | 'createImportQueue'
  | 'createProject'
  | 'deleteBook'
  | 'deleteBookCover'
  | 'deleteChapter'
  | 'deleteProject'
  | 'getAccountQuota'
  | 'getBookExport'
  | 'getBookDrawingCounts'
  | 'setBookCoverFromImage'
  | 'openChapterEdit'
  | 'getBook'
  | 'getChapter'
  | 'getImportConstraints'
  | 'getImportQueue'
  | 'getIndexingStatus'
  | 'getAgentTimeline'
  | 'getJob'
  | 'getProject'
  | 'importBookFile'
  | 'listBooks'
  | 'listChapters'
  | 'listChatSessions'
  | 'listImportQueues'
  | 'listProjectJobs'
  | 'listProjectMembers'
  | 'listProjects'
  | 'logout'
	| 'restoreBook'
	| 'listRecentlyDeleted'
	| 'previewBookUpdateRevert'
	| 'revertBookUpdate'
	| 'restoreChapter'
	| 'restoreProject'
  | 'searchProject'
  | 'searchChapters'
  | 'skipImportQueueItem'
  | 'startImportQueue'
  | 'startProjectIndexing'
  | 'updateBook'
  | 'updateProject'
  | 'uploadImportQueueItem'
  | 'uploadBookCover'
>;

type ManuscriptShelfClient = Pick<ManuscriptApiClient, 'getProject' | 'listBooks' | 'listProjectJobs' | 'listProjects'>;
type FirstProjectClient = Pick<ManuscriptApiClient, 'createProject' | 'listBooks' | 'listProjectJobs'>;
type IndexingStatusClient = Pick<ManuscriptApiClient, 'getIndexingStatus'>;
const shelfReconciliationIntervalMs = 2_000;

export type ChapterListItem = Awaited<ReturnType<ManuscriptApiClient['listChapters']>>['data'][number];
export type ProjectJob = Job;
export type SearchResult = Awaited<ReturnType<ManuscriptApiClient['searchProject']>>['data'][number];
export type ChapterNavigationLocator = Schema<'ChapterNavigationLocator'>;
export type SearchTextCoverage = Awaited<ReturnType<ManuscriptApiClient['searchProject']>>['textCoverage'];
export type IndexingStatus = Awaited<ReturnType<ManuscriptApiClient['getIndexingStatus']>>;
export type ImportConstraints = Awaited<ReturnType<ManuscriptApiClient['getImportConstraints']>>;
export type IndexingStep = IndexingStatus['steps'][number];
/** «Недавно удалённые» over all the author's sagas (specs/2026-11-11-ux7-trash.md TB1.1). */
export type RecentlyDeleted = Schema<'RecentlyDeleted'>;
/** What «Вернуть прежнюю версию» of a book would do (specs/2026-11-12-ux10-book-revert.md BR2.1). */
export type BookRevertPreview = Schema<'BookRevertPreview'>;
export type RestoredSource =
  | { kind: 'project'; resource: Project }
  | { kind: 'book'; resource: Book }
  | { kind: 'chapter'; resource: Chapter };

export interface ManuscriptShelfWorkspace {
  books: Book[];
  canEditManuscript: boolean;
  /** Jobs in work of the project: queued, running or canceling. */
  jobs: ProjectJob[];
  project: Project | null;
  projects: Project[];
}

export type CreateProjectInput = Parameters<ManuscriptApiClient['createProject']>[0];
export type FirstProjectWorkspace = Omit<ManuscriptShelfWorkspace, 'project' | 'projects'> & {
  project: Project;
  projects: Project[];
};

export type ManuscriptNotFound = 'project' | 'book' | 'chapter';

export interface ManuscriptReaderWorkspace {
  book: Book | null;
  chapter: ReaderChapter | null;
  chapters: ChapterListItem[];
  loadError?: unknown;
  notFound?: ManuscriptNotFound;
  project: Project | null;
  projects: Project[];
}

/** Text consumed by the Reader, without editable Chapter persistence fields. */
export type ReaderChapter = Pick<Chapter, 'id' | 'bookId' | 'title' | 'contentVariant' | 'revision' | 'wordCount' | 'navigation'> & {
  /** `markdown` carries the italic and bold of a paragraph (specs/2026-11-08-italic-bold-in-text.md TF5.1). */
  paragraphs: Array<Pick<Chapter['paragraphs'][number], 'id' | 'text'> & Partial<Pick<Chapter['paragraphs'][number], 'kind' | 'markdown'>>>;
};

export interface ManuscriptWorkspaceParams {
  bookId?: string | null;
  chapterId?: string | null;
  projectId?: string | null;
}

export function selectPublishedChapter(chapters: ChapterListItem[], chapterId?: string | null) {
  const exactChapter = chapterId ? chapters.find((item) => item.id === chapterId) : undefined;
  if (chapterId && !exactChapter) return null;
  return (exactChapter && (exactChapter.status === 'published' || exactChapter.status === 'stale') ? exactChapter : undefined)
    ?? chapters.find((item) => item.status === 'published' || item.status === 'stale')
    ?? null;
}

/**
 * `follow` re-reads the status while the saga is being prepared: at the pace of the shelf card, or every so many
 * milliseconds.
 */
type IndexingStatusQueryOptions =
  | { enabled?: boolean; follow?: boolean | number; untilText?: boolean; bookId?: undefined; sourceView?: undefined }
  | { enabled?: boolean; follow?: boolean | number; untilText?: boolean; bookId: string; sourceView: SourceView };

export const manuscriptQueryKeys = {
  all: ['canonkeeper', 'manuscript'] as const,
  readerWorkspace: (params: ManuscriptWorkspaceParams) =>
    [...manuscriptQueryKeys.all, 'reader-workspace', normalizeKey(params.projectId), normalizeKey(params.bookId), normalizeKey(params.chapterId)] as const,
  sourceChapter: (chapterId: string) => [...manuscriptQueryKeys.all, 'source-chapter', chapterId] as const,
  shelf: (params: Pick<ManuscriptWorkspaceParams, 'projectId'>) => [...manuscriptQueryKeys.all, 'shelf', params.projectId ?? null] as const,
  importConstraints: (projectId: string) => [...manuscriptQueryKeys.all, 'import-constraints', projectId] as const,
  importQueues: (projectId: string) => [...manuscriptQueryKeys.all, 'import-queues', projectId] as const,
  importQueue: (projectId: string, queueId: string) => [...manuscriptQueryKeys.importQueues(projectId), queueId] as const,
  recentlyDeleted: () => [...manuscriptQueryKeys.all, 'recently-deleted'] as const,
  /** What a return of the book to the version before its update would do now (BR2.1). */
  bookRevert: (bookId: string) => [...manuscriptQueryKeys.all, 'book-revert', bookId] as const,
  bookDrawings: (projectId: string) => [...manuscriptQueryKeys.all, 'book-drawings', projectId] as const,
  indexingStatus: (projectId: string, scope?: { bookId: string; sourceView: SourceView }) => scope
    ? [...manuscriptQueryKeys.all, 'indexing-status', projectId, scope.bookId, scope.sourceView] as const
    : [...manuscriptQueryKeys.all, 'indexing-status', projectId] as const,
  job: (jobId: string) => [...manuscriptQueryKeys.all, 'job', jobId] as const,
  /** Whether others share the saga: the first two members (specs/2026-11-11-ux9-shelf-chat.md SC1.1). */
  members: (projectId: string) => [...manuscriptQueryKeys.all, 'members', projectId] as const,
};

interface ManuscriptApiQueryMeta<T> extends Record<string, unknown> {
  manuscriptApi: T;
}

function manuscriptApiMeta<T>(api: T): ManuscriptApiQueryMeta<T> {
  return { manuscriptApi: api };
}

function manuscriptApiFromMeta<T>(meta: unknown) {
  const queryMeta = meta as Partial<ManuscriptApiQueryMeta<T>> | undefined;
  if (!queryMeta?.manuscriptApi) {
    throw new Error('Manuscript query is missing CanonKeeper API client metadata.');
  }
  return queryMeta.manuscriptApi;
}

function normalizeKey(value: string | null | undefined) {
  return value ?? '';
}

export function selectProject(projects: Project[], projectId?: string | null) {
  return projectId === null || projectId === undefined
    ? projects[0] ?? null
    : projects.find((item) => item.id === projectId) ?? null;
}

function preferredBook(books: Book[]) {
  return books
    .filter((item) => item.status === 'ready')
    .sort((left, right) => right.chapterCount - left.chapterCount || left.order - right.order)[0] ?? books[0] ?? null;
}

export function selectBook(books: Book[], bookId: string | null | undefined) {
  return bookId ? books.find((item) => item.id === bookId) ?? null : preferredBook(books);
}

export type ProjectRole = ProjectMembership['role'];

export function projectRole(project: Project | null | undefined): ProjectRole | null {
  return project?.currentMembership.role ?? null;
}

export function canEditProject(project: Project | null | undefined) {
  const role = projectRole(project);
  return role === 'owner' || role === 'admin' || role === 'editor';
}

/** Import publishes every book at once, so the app reads one text for everyone: the published one. */
export const textSourceView: SourceView = 'published';

/** The import that rewrites an existing book from a new file (specs/2026-10-27-update-book-from-file.md). */
export function isBookUpdateJob(job: Job) {
  return job.subject.type === 'import' && Boolean(job.subject.bookId) && !job.subject.preview;
}

/** An import of its own, outside the import queue, that writes: a book update or a new book such as the example. */
export function isSingleImportJob(job: Job) {
  return job.subject.type === 'import' && !job.subject.importQueueId && !job.subject.preview;
}

/** The preview of a book update: it writes nothing, so the shelf does not show it as work on the book (R6.7). */
export function isBookUpdatePreview(job: Job) {
  return job.subject.type === 'import' && job.subject.preview === true;
}

/**
 * The shelf follows only Jobs in work: the active import, text index, cards, thread layer and book update
 * (specs/2026-10-28-fast-job-list-and-postgres.md, UC1; specs/2026-11-08-deletion-stops-processing.md, UC3).
 */
const shelfJobsQuery = { active: true } as const;

/** The shelf of a saga with every saga and book of their lists (specs/2026-11-07-chapter-list-paging.md, P2.1). */
async function loadShelfWorkspace(api: ManuscriptShelfClient, params: Pick<ManuscriptWorkspaceParams, 'projectId'>): Promise<ManuscriptShelfWorkspace> {
  const projectList = await readAllPages((cursor) => api.listProjects({ cursor }));
  const projectSummary = selectProject(projectList.data, params.projectId);
  if (!projectSummary) {
    return { books: [], canEditManuscript: false, jobs: [], project: null, projects: projectList.data };
  }

  const [project, bookList, jobList] = await Promise.all([
    api.getProject(projectSummary.id),
    readAllPages((cursor) => api.listBooks(projectSummary.id, { cursor })),
    api.listProjectJobs(projectSummary.id, shelfJobsQuery).catch(() => ({ data: [] })),
  ]);

  return {
    books: bookList.data,
    canEditManuscript: canEditProject(project),
    jobs: jobList.data.filter((job) => !isBookUpdatePreview(job)),
    project,
    projects: projectList.data,
  };
}

export async function createFirstProjectWorkspace(api: FirstProjectClient, body: CreateProjectInput): Promise<FirstProjectWorkspace> {
  const project = await api.createProject(body);
  const [bookList, jobList] = await Promise.all([
    api.listBooks(project.id),
    api.listProjectJobs(project.id, shelfJobsQuery),
  ]);

  return {
    books: bookList.data,
    canEditManuscript: canEditProject(project),
    jobs: jobList.data,
    project,
    projects: [project],
  };
}

export function useManuscriptShelfQuery(api: ManuscriptShelfClient, params: Pick<ManuscriptWorkspaceParams, 'projectId'>) {
  return useQuery({
    queryKey: manuscriptQueryKeys.shelf(params),
    queryFn: ({ meta }) => loadShelfWorkspace(manuscriptApiFromMeta<ManuscriptShelfClient>(meta), params),
    meta: manuscriptApiMeta(api),
    // An address that takes the id of the saga the page already shows — the one the server chose for an address without
    // a saga — keeps that shelf while the shelf of the id loads: the page does not start over, and what it was doing
    // (the first question of a chat, a drawing just begun) goes on. Another saga never stands in for the one asked.
    placeholderData: (previous) => (params.projectId && previous?.project?.id === params.projectId ? previous : undefined),
    // A single import outside the import queue — a book update, the book of the example (specs/2026-11-11-import-speed.md
    // IS2.2) — is followed by the shelf too; a stopping Job is followed until the server ends it
    // (specs/2026-11-08-deletion-stops-processing.md R3.5).
    refetchInterval: (query) => query.state.data?.jobs.some((job) => (
      (job.kind === 'canon_text_index' || job.kind === 'chapter_cards' || job.kind === 'saga_layer' || isSingleImportJob(job))
      && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling')
    )) ? shelfReconciliationIntervalMs : false,
  });
}

/**
 * How many drawings the files of the books of a saga would carry (specs/2026-11-12-ux10-book-pictures.md BP3.1, BP3.2): read
 * again whenever «Скачать книгу…» or «Скачать всю сагу…» opens, as the server counts them for the file.
 */
export function useBookDrawingCountsQuery(api: Pick<ManuscriptApiClient, 'getBookDrawingCounts'>, projectId: string, enabled: boolean) {
  return useQuery({
    queryKey: manuscriptQueryKeys.bookDrawings(projectId),
    queryFn: ({ meta, signal }) => manuscriptApiFromMeta<Pick<ManuscriptApiClient, 'getBookDrawingCounts'>>(meta).getBookDrawingCounts(projectId, { signal }),
    meta: manuscriptApiMeta(api),
    enabled: enabled && projectId.length > 0,
    staleTime: 0,
    refetchOnMount: 'always',
  });
}

/** Prepare the target workspace before changing the current navigation context. */
export function useSelectManuscriptProject(api: ManuscriptShelfClient) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (projectId: string) => {
      const params = { projectId };
      const workspace = await client.fetchQuery({
        queryKey: manuscriptQueryKeys.shelf(params),
        queryFn: ({ meta }) => loadShelfWorkspace(manuscriptApiFromMeta<ManuscriptShelfClient>(meta), params),
        meta: manuscriptApiMeta(api), staleTime: 0, retry: false,
      });
      if (projectId && workspace.project?.id !== projectId) throw new Error('Сага недоступна.');
      return workspace;
    },
    meta: { outageScope: 'local' },
  });
}

/**
 * The server still works on what the status is about: a text version is being prepared, the saga has a preparation to
 * tell of, or a step of the book is queued, running or being stopped. A reading that waits for the helper is no work: the
 * status stays as it is until the helper comes (specs/2026-11-12-desk-window.md WN3.2).
 */
function statusInWork(status: IndexingStatus | undefined) {
  if (!status) return false;
  return status.draft?.context?.state === 'processing' || status.published?.context?.state === 'processing'
    || Boolean(status.preparation && !status.preparation.waiting)
    || status.steps.some((step) => step.status === 'queued' || step.status === 'running' || step.status === 'canceling');
}

export function useIndexingStatusQuery(
  api: IndexingStatusClient,
  projectId: string,
  options: IndexingStatusQueryOptions = {},
) {
  const scope = options.bookId ? { bookId: options.bookId, sourceView: options.sourceView } : undefined;
  return useQuery({
    queryKey: manuscriptQueryKeys.indexingStatus(projectId, scope),
    queryFn: ({ meta, signal }) => manuscriptApiFromMeta<IndexingStatusClient>(meta).getIndexingStatus(projectId, { ...scope, signal }),
    meta: manuscriptApiMeta(api),
    enabled: (options.enabled ?? true) && projectId.length > 0,
    // The chat and the card of a book wait for the preparation to end (R2.3.3; FS5.8): between the volumes of an import
    // queue the text is ready and the saga is still prepared — the server says so with `preparation`. A saga whose new
    // book is still put on the shelf is read until it has text (specs/2026-11-11-import-speed.md IS2.2).
    refetchInterval: (query) => {
      const status = query.state.data;
      if (options.follow && statusInWork(status)) return options.follow === true ? shelfReconciliationIntervalMs : options.follow;
      return options.untilText && status?.[textSourceView]?.exists !== true ? shelfReconciliationIntervalMs : false;
    },
  });
}

/**
 * The saga the page shows, for what watches it beside the page: the project of the address, else the saga the shelf of
 * the page has read for an address without one — and whether that shelf has an import, a text index or cards in work.
 * It reads the cache of the page and asks the server nothing.
 */
export function useShownSaga(requestedProjectId: string | null) {
  const client = useQueryClient();
  // One string, so the snapshot stays the same value while nothing of it changes.
  const shown = useSyncExternalStore(
    (listener) => client.getQueryCache().subscribe(listener),
    () => {
      const shelf = client.getQueryData<ManuscriptShelfWorkspace>(manuscriptQueryKeys.shelf({ projectId: requestedProjectId }));
      const preparing = shelf?.jobs.some((job) => job.kind === 'import' || job.kind === 'canon_text_index' || job.kind === 'chapter_cards') ?? false;
      return `${preparing ? '1' : '0'}${shelf?.project?.id ?? ''}`;
    },
  );
  return { preparing: shown.startsWith('1'), projectId: requestedProjectId ?? (shown.slice(1) || null) };
}
