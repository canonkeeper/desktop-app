import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
  | 'listProjects'
  | 'logout'
	| 'restoreBook'
	| 'restoreChapter'
	| 'restoreProject'
  | 'searchProject'
  | 'searchChapters'
  | 'startImportQueue'
  | 'startProjectIndexing'
  | 'updateBook'
  | 'updateProject'
  | 'updateImportQueue'
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
export type DeletedProject = Schema<'DeletedProject'>;
export type DeletedBook = Schema<'DeletedBook'>;
export type DeletedChapter = Schema<'DeletedChapter'>;
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
  paragraphs: Array<Pick<Chapter['paragraphs'][number], 'id' | 'text'> & Partial<Pick<Chapter['paragraphs'][number], 'kind'>>>;
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

/** `follow` re-reads the status at the pace of the shelf card while the saga is being prepared. */
type IndexingStatusQueryOptions =
  | { enabled?: boolean; follow?: boolean; bookId?: undefined; sourceView?: undefined }
  | { enabled?: boolean; follow?: boolean; bookId: string; sourceView: SourceView };

export const manuscriptQueryKeys = {
  all: ['canonkeeper', 'manuscript'] as const,
  readerWorkspace: (params: ManuscriptWorkspaceParams) =>
    [...manuscriptQueryKeys.all, 'reader-workspace', normalizeKey(params.projectId), normalizeKey(params.bookId), normalizeKey(params.chapterId)] as const,
  sourceChapter: (chapterId: string) => [...manuscriptQueryKeys.all, 'source-chapter', chapterId] as const,
  shelf: (params: Pick<ManuscriptWorkspaceParams, 'projectId'>) => [...manuscriptQueryKeys.all, 'shelf', params.projectId ?? null] as const,
  importConstraints: (projectId: string) => [...manuscriptQueryKeys.all, 'import-constraints', projectId] as const,
  importQueues: (projectId: string) => [...manuscriptQueryKeys.all, 'import-queues', projectId] as const,
  importQueue: (projectId: string, queueId: string) => [...manuscriptQueryKeys.importQueues(projectId), queueId] as const,
  deletedProjects: () => [...manuscriptQueryKeys.all, 'deleted', 'projects'] as const,
  deletedBooks: (projectId: string | null | undefined) =>
    [...manuscriptQueryKeys.all, 'deleted', 'books', normalizeKey(projectId)] as const,
  deletedChapters: (bookId: string | null | undefined) =>
    [...manuscriptQueryKeys.all, 'deleted', 'chapters', normalizeKey(bookId)] as const,
  indexingStatus: (projectId: string, scope?: { bookId: string; sourceView: SourceView }) => scope
    ? [...manuscriptQueryKeys.all, 'indexing-status', projectId, scope.bookId, scope.sourceView] as const
    : [...manuscriptQueryKeys.all, 'indexing-status', projectId] as const,
  job: (jobId: string) => [...manuscriptQueryKeys.all, 'job', jobId] as const,
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

/** The preview of a book update: it writes nothing, so the shelf does not show it as work on the book (R6.7). */
export function isBookUpdatePreview(job: Job) {
  return job.subject.type === 'import' && job.subject.preview === true;
}

export function jobReferencesBook(job: Job, bookId: string) {
  if (job.subject.type === 'import') {
    return job.subject.bookId === bookId || (job.result?.type === 'import' && job.result.bookId === bookId);
  }
  if (job.subject.type === 'canon_text_index') {
    return job.status === 'queued' || job.status === 'running' || job.status === 'canceling';
  }
  return 'bookId' in job.subject && job.subject.bookId === bookId;
}

/**
 * The shelf follows only Jobs in work: the active import, text index, cards and book update
 * (specs/2026-10-28-fast-job-list-and-postgres.md, UC1).
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
    // A book update is a single import outside the import queue, so the shelf follows it too.
    refetchInterval: (query) => query.state.data?.jobs.some((job) => (
      (job.kind === 'canon_text_index' || job.kind === 'chapter_cards' || isBookUpdateJob(job))
      && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling')
    )) ? shelfReconciliationIntervalMs : false,
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
    // The chat waits for the preparation of its text version to end (R2.3.3).
    refetchInterval: (query) => options.follow && query.state.data?.[textSourceView]?.context?.state === 'processing'
      ? shelfReconciliationIntervalMs
      : false,
  });
}
