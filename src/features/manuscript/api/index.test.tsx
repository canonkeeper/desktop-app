import type { PropsWithChildren } from 'react';
import { accountQuotaQueryKeys } from '../../../entities/account-quota/api';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { paginateList } from '../../../api/mock/repository/common';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import type { ImportQueue } from '../../../shared/api';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import {
  useCreateBookMutation,
  useDeleteBookMutation,
  useCancelImportQueueMutation,
  useCreateImportQueueMutation,
  useRestartIndexingMutation,
  useSearchProjectMutation,
  useSkipImportQueueItemMutation,
  useStartImportQueueMutation,
  useUploadImportQueueItemMutation,
} from './index';
import {
  manuscriptQueryKeys,
  useIndexingStatusQuery,
  useManuscriptShelfQuery,
} from '../../../entities/manuscript/api';
import {
  useManuscriptReaderWorkspaceQuery,
  useImportQueueQuery,
  useImportQueuesQuery,
} from './index';
import type { ManuscriptShelfWorkspace } from '../../../entities/manuscript/api/queries';

describe('manuscript indexing API', () => {
  it('passes the optional exact-book search filter through the shared mutation', async () => {
    const searchProject = vi.fn(async () => ({ items: [], textCoverage: null }));
    const api = { searchProject } as unknown as ManuscriptApiClient;
    const queryClient = new QueryClient();
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useSearchProjectMutation(api), { wrapper });

    await act(() => result.current.mutateAsync({
      projectId: 'project-1',
      query: 'маяк',
      sourceView: 'published',
      scope: 'all',
      filters: { bookId: 'book-1' },
    }));

    expect(searchProject).toHaveBeenCalledWith(
      'project-1',
      'маяк',
      'published',
      'all',
      { filters: { bookId: 'book-1' } },
    );
  });

  it('does not substitute the first project for an explicit unknown project id', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const getProject = vi.fn(async () => ({ id: 'project-first' }) as never);
    const listBooks = vi.fn(async () => ({ data: [], meta: { hasMore: false, nextCursor: null } }) as never);
    const listProjectJobs = vi.fn(async () => ({ data: [] }) as never);
    const api = {
      getProject,
      listBooks,
      listProjectJobs,
      listProjects: vi.fn(async () => ({ data: [{ id: 'project-first' }], meta: { hasMore: false, nextCursor: null } }) as never),
    } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(
      () => useManuscriptShelfQuery(api, { projectId: 'project-missing' }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data).toMatchObject({ project: null, projects: [{ id: 'project-first' }] });
    expect(getProject).not.toHaveBeenCalled();
    expect(listBooks).not.toHaveBeenCalled();
    expect(listProjectJobs).not.toHaveBeenCalled();
  });

  it('reads only the Jobs in work for the shelf', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const listProjectJobs = vi.fn(async () => ({ data: [] }) as never);
    const api = {
      getProject: vi.fn(async () => ({ id: 'project-first', currentMembership: { role: 'owner' } }) as never),
      listBooks: vi.fn(async () => ({ data: [], meta: { hasMore: false, nextCursor: null } }) as never),
      listProjectJobs,
      listProjects: vi.fn(async () => ({ data: [{ id: 'project-first' }], meta: { hasMore: false, nextCursor: null } }) as never),
    } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useManuscriptShelfQuery(api, { projectId: 'project-first' }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(listProjectJobs).toHaveBeenCalledWith('project-first', { active: true });
  });

  it('adds a created book to the exact shelf cache without waiting for refetch', async () => {
    const queryClient = new QueryClient();
    const shelfKey = manuscriptQueryKeys.shelf({ projectId: 'project-1' });
    queryClient.setQueryData(
      shelfKey,
      { books: [], jobs: [], project: null, projects: [] } as unknown as ManuscriptShelfWorkspace,
    );
    const created = { id: 'book-created', projectId: 'project-1', title: 'Новая книга' };
    const api = { createBook: vi.fn(async () => created) } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useCreateBookMutation(api), { wrapper });

    await act(() => result.current.mutateAsync({
      projectId: 'project-1',
      body: { title: 'Новая книга' },
    }));

    expect(queryClient.getQueryData<ManuscriptShelfWorkspace>(shelfKey)?.books).toEqual([created]);
  });

  // specs/2026-11-08-deletion-stops-processing.md R3.4: the Jobs are the server's; a stopped one shows as stopping.
  it('removes only the deleted book from the exact shelf cache and leaves its Jobs to the server', async () => {
    const queryClient = new QueryClient();
    const shelfKey = manuscriptQueryKeys.shelf({ projectId: 'project-1' });
    const keptBook = { id: 'book-kept', projectId: 'project-1', title: 'Книга' };
    const deletedBook = { id: 'book-deleted', projectId: 'project-1', title: 'Черновик' };
    const keptJob = {
      id: 'job-kept',
      kind: 'research_chat_turn',
      subject: { type: 'research_chat_turn', scope: 'book', bookId: 'book-kept', projectId: 'project-1' },
    };
    const deletedJob = {
      id: 'job-deleted',
      kind: 'research_chat_turn',
      subject: { type: 'research_chat_turn', scope: 'book', bookId: 'book-deleted', projectId: 'project-1' },
    };
    const deletedTextIndexJob = {
      id: 'job-text-index-deleted-source',
      kind: 'canon_text_index',
      status: 'running',
      subject: {
        type: 'canon_text_index',
        projectId: 'project-1',
        sourceView: 'draft',
        catalogRevision: 7,
      },
    };
    const keptPublishedTextIndexJob = {
      id: 'job-text-index-other-view',
      kind: 'canon_text_index',
      status: 'running',
      subject: {
        type: 'canon_text_index',
        projectId: 'project-1',
        sourceView: 'published',
        catalogRevision: 9,
      },
    };
    const keptTerminalTextIndexJob = {
      id: 'job-text-index-terminal',
      kind: 'canon_text_index',
      status: 'succeeded',
      subject: {
        type: 'canon_text_index',
        projectId: 'project-1',
        sourceView: 'draft',
        catalogRevision: 8,
      },
    };
    queryClient.setQueryData(shelfKey, {
      books: [keptBook, deletedBook],
      jobs: [keptJob, deletedJob, deletedTextIndexJob, keptPublishedTextIndexJob, keptTerminalTextIndexJob],
      project: null,
      projects: [],
    } as unknown as ManuscriptShelfWorkspace);
    const api = { deleteBook: vi.fn(async () => ({})) } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useDeleteBookMutation(api), { wrapper });

    await act(() => result.current.mutateAsync({ bookId: 'book-deleted', projectId: 'project-1' }));

    const shelf = queryClient.getQueryData<ManuscriptShelfWorkspace>(shelfKey);
    expect(shelf?.books).toEqual([keptBook]);
    expect(shelf?.jobs).toEqual([keptJob, deletedJob, deletedTextIndexJob, keptPublishedTextIndexJob, keptTerminalTextIndexJob]);
    expect(queryClient.getQueryState(shelfKey)?.isInvalidated).toBe(true);
  });

  it('keeps the backend indexing projection at the entity query boundary', async () => {
    const queryClient = new QueryClient();
    const getIndexingStatus = vi.fn(async () => ({
      published: { exists: true, context: { state: 'processing', currentBook: null, retryableFailedBook: null } },
      steps: [{ kind: 'enrichment', status: 'running', progress: null }],
    }) as never);
    const api = { getIndexingStatus } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useIndexingStatusQuery(api, 'project-1', {
      bookId: 'book-1',
      sourceView: 'draft',
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.steps).toEqual([{ kind: 'enrichment', status: 'running', progress: null }]);
    expect(getIndexingStatus).toHaveBeenCalledWith('project-1', {
      bookId: 'book-1',
      signal: expect.any(AbortSignal),
      sourceView: 'draft',
    });
  });

  it('does not poll indexing status while lifecycle updates drive invalidation', async () => {
    vi.useFakeTimers();
    try {
      const getIndexingStatus = vi.fn(async () => ({
        published: { exists: true, context: { state: 'processing', currentBook: null, retryableFailedBook: null } },
        steps: [{ kind: 'text_index', status: 'running', progress: null }],
      }) as never);
      const api = { getIndexingStatus } as unknown as ManuscriptApiClient;
      const queryClient = new QueryClient();
      const wrapper = ({ children }: PropsWithChildren) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      );
      renderHook(() => useIndexingStatusQuery(api, 'project-live'), { wrapper });

      await act(async () => { await Promise.resolve(); });
      expect(getIndexingStatus).toHaveBeenCalledTimes(1);

      await act(async () => { await vi.advanceTimersByTimeAsync(6_000); });
      expect(getIndexingStatus).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  // specs/2026-11-12-desk-window.md WN3.2: a followed status is read again while the helper reads the saga, and not while
  // the reading waits for the helper — nobody reads, and nothing comes until the helper does.
  it.each([
    ['is read', undefined, true],
    ['waits for the helper', 'helper' as const, false],
  ])('follows a reading of the saga that %s only while someone reads', async (_what, waiting, follows) => {
    vi.useFakeTimers();
    try {
      const getIndexingStatus = vi.fn(async () => ({
        published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } },
        steps: [],
        preparation: {
          remainingSeconds: waiting ? null : 300, letterPromised: false, chaptersRead: 0, chaptersTotal: 2, booksRead: 0,
          booksTotal: 1, currentBookId: 'book-1', askable: true, books: [{ bookId: 'book-1', chaptersRead: 0, chaptersTotal: 2 }],
          ...(waiting ? { waiting } : {}),
        },
      }) as never);
      const api = { getIndexingStatus } as unknown as ManuscriptApiClient;
      const queryClient = new QueryClient();
      const wrapper = ({ children }: PropsWithChildren) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      );
      renderHook(() => useIndexingStatusQuery(api, 'project-waiting', { follow: true }), { wrapper });

      await act(async () => { await Promise.resolve(); });
      expect(getIndexingStatus).toHaveBeenCalledTimes(1);

      await act(async () => { await vi.advanceTimersByTimeAsync(6_000); });
      if (follows) expect(getIndexingStatus.mock.calls.length).toBeGreaterThan(1);
      else expect(getIndexingStatus).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('binds indexing status cache entries to project, book and source view', () => {
    expect(manuscriptQueryKeys.indexingStatus('project-1')).toEqual([
      'canonkeeper',
      'manuscript',
      'indexing-status',
      'project-1',
    ]);
    expect(manuscriptQueryKeys.indexingStatus('project-1', { bookId: 'book-1', sourceView: 'draft' })).toEqual([
      'canonkeeper',
      'manuscript',
      'indexing-status',
      'project-1',
      'book-1',
      'draft',
    ]);
  });

  it('restarts one text version and invalidates only its current surfaces', async () => {
    const queryClient = new QueryClient();
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue();
    const startProjectIndexing = vi.fn(async () => ({}) as never);
    const api = { startProjectIndexing } as unknown as ManuscriptApiClient;
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useRestartIndexingMutation(api), { wrapper });
    const input = { projectId: 'project-1', sourceView: 'draft' as const };

    await act(() => result.current.mutateAsync(input));

    expect(startProjectIndexing).toHaveBeenCalledWith(
      'project-1',
      { sourceView: 'draft' },
      { idempotencyKey: expect.stringMatching(/^startProjectIndexing:/) },
    );
    expect(invalidateQueries.mock.calls).toEqual([
      [{ queryKey: manuscriptQueryKeys.indexingStatus('project-1'), exact: false }],
      [{ queryKey: manuscriptQueryKeys.shelf({ projectId: 'project-1' }), exact: true }],
    ]);
  });
});

describe('manuscript workspace API', () => {
  it.each(['book-missing', 'book-foreign'])(
    'does not substitute a preferred book for explicit unavailable book %s',
    async (requestedBookId) => {
      const api = createMockApiClient();
      const getChapter = vi.spyOn(api, 'getChapter');
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const wrapper = ({ children }: PropsWithChildren) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      );
      const { result } = renderHook(() => useManuscriptReaderWorkspaceQuery(api, {
        bookId: requestedBookId,
        projectId,
      }), { wrapper });

      await waitFor(() => expect(result.current.isSuccess).toBe(true));

      expect(result.current.data).toMatchObject({ book: null, chapter: null, chapters: [] });
      expect(getChapter).not.toHaveBeenCalled();
    },
  );

  it('reads every page of a long book: chapter 120 opens by its link and the contents hold all 120', async () => {
    const api = createMockApiClient();
    const chapters = Array.from({ length: 120 }, (_, index) => ({
      id: `chapter-long-${index + 1}`,
      bookId,
      title: `Глава ${index + 1}`,
      order: index + 1,
      displayNumber: String(index + 1),
      status: 'published' as const,
      wordCount: 10,
      hasDraft: false,
    }));
    vi.spyOn(api, 'listChapters').mockImplementation((async (id: string, options: { cursor?: string | null; limit?: number } = {}) =>
      paginateList('listChapters', chapters, { cursor: options.cursor, limit: options.limit }, { bookId: id, state: 'active' })) as never);
    const getChapter = vi.spyOn(api, 'getChapter').mockImplementation(async (id) => ({ id }) as never);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useManuscriptReaderWorkspaceQuery(api, {
      bookId,
      chapterId: 'chapter-long-120',
      projectId,
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.chapters.map((chapter) => chapter.id)).toEqual(chapters.map((chapter) => chapter.id));
    expect(getChapter).toHaveBeenCalledWith('chapter-long-120', 'published');
    expect(result.current.data?.chapter).toMatchObject({ id: 'chapter-long-120' });
  });

  it.each(['chapter-missing', 'chapter-03-01'])(
    'does not substitute a published chapter for explicit unavailable reader chapter %s',
    async (requestedChapterId) => {
      const api = createMockApiClient();
      const getChapter = vi.spyOn(api, 'getChapter');
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const wrapper = ({ children }: PropsWithChildren) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      );
      const { result } = renderHook(() => useManuscriptReaderWorkspaceQuery(api, {
        bookId,
        chapterId: requestedChapterId,
        projectId,
      }), { wrapper });

      await waitFor(() => expect(result.current.isSuccess).toBe(true));

      expect(result.current.data?.chapter).toBeNull();
      expect(getChapter).not.toHaveBeenCalled();
    },
  );
});

describe('durable import queue API', () => {
  const queue: ImportQueue = {
    id: 'queue-1',
    projectId: 'project-1',
    status: 'running',
    version: 2,
    intendedItemCount: 1,
    currentItemId: null,
    items: [],
    createdAt: '2026-08-20T10:00:00.000Z',
    updatedAt: '2026-08-20T10:00:01.000Z',
    startedAt: '2026-08-20T10:00:01.000Z',
    completedAt: null,
    links: { self: '/projects/project-1/import-queues/queue-1', cancel: null },
  };
  const stoppingQueue: ImportQueue = {
    ...queue,
    status: 'canceled',
    version: 3,
    currentItemId: 'item-1',
    items: [{
      id: 'item-1',
      clientItemId: 'client-item-1',
      position: 1,
      sourceFileName: 'book.fb2',
      mimeType: 'application/x-fictionbook+xml',
      sizeBytes: 1024,
      preflightStatus: 'ready',
      preflightError: null,
      processingStatus: 'canceling',
      failureClass: null,
      failedStep: null,
      importJobId: 'import-1',
      bookId: 'book-1',
      updatedAt: '2026-08-20T10:00:02.000Z',
      links: { status: '/manuscript/books?projectId=project-1&bookId=book-1&sourceView=draft' },
    }],
    updatedAt: '2026-08-20T10:00:02.000Z',
    completedAt: '2026-08-20T10:00:02.000Z',
  };
  const createBody = {
    items: [{ clientItemId: 'client-item-1', position: 1, sourceFileName: 'book.fb2' }],
  };
  const volume = stoppingQueue.items[0]!;
  const workingQueue: ImportQueue = { ...queue, items: [{ ...volume, processingStatus: 'importing' }] };
  // specs/2026-11-11-ux7-upload.md UP3.2: a running queue whose one volume left waits for the author changes by nothing
  // but the author.
  const waitingQueue: ImportQueue = {
    ...queue,
    items: [{ ...volume, processingStatus: 'failed' }, { ...volume, id: 'item-2', processingStatus: 'succeeded' }],
  };

  function createWrapper(queryClient: QueryClient) {
    return ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  }

  it('fetches queue projections with keys, signals and active-only reconciliation', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const api = {
      getImportQueue: vi.fn(async () => queue),
      listImportQueues: vi.fn(async () => ({ data: [queue] })),
    } as unknown as ManuscriptApiClient;
    const wrapper = createWrapper(queryClient);

    const list = renderHook(() => useImportQueuesQuery(api, 'project-1'), { wrapper });
    const detail = renderHook(() => useImportQueueQuery(api, 'project-1', 'queue-1'), { wrapper });

    await waitFor(() => expect(list.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(detail.result.current.isSuccess).toBe(true));

    expect(api.listImportQueues).toHaveBeenCalledWith('project-1', { signal: expect.any(AbortSignal) });
    expect(api.getImportQueue).toHaveBeenCalledWith('project-1', 'queue-1', { signal: expect.any(AbortSignal) });
    const listQuery = queryClient.getQueryCache().find({ queryKey: manuscriptQueryKeys.importQueues('project-1') });
    const detailQuery = queryClient.getQueryCache().find({ queryKey: manuscriptQueryKeys.importQueue('project-1', 'queue-1') });
    expect(listQuery?.queryKey).toEqual(['canonkeeper', 'manuscript', 'import-queues', 'project-1']);
    expect(detailQuery?.queryKey).toEqual(['canonkeeper', 'manuscript', 'import-queues', 'project-1', 'queue-1']);
    const listInterval = (listQuery?.options as unknown as {
      refetchInterval?: (query: { state: { data?: { data: ImportQueue[] } } }) => unknown;
    }).refetchInterval;
    expect(listInterval?.({
      state: { data: { data: [workingQueue] } },
    })).toBe(2_000);
    expect(listInterval?.({
      state: { data: { data: [stoppingQueue] } },
    })).toBe(false);
    expect(listInterval?.({
      state: { data: { data: [waitingQueue] } },
    })).toBe(false);
    expect((detailQuery?.options as unknown as { refetchInterval?: unknown }).refetchInterval).toBeUndefined();
  });

  it('invalidates exact queue and shelf surfaces after every queue mutation', async () => {
    const expectedInvalidations = [
      // Uploads and books change the writer's quota (quota spec R5.2).
      { queryKey: accountQuotaQueryKeys.all, exact: true },
      { queryKey: manuscriptQueryKeys.shelf({ projectId: 'project-1' }), exact: true },
      { queryKey: manuscriptQueryKeys.indexingStatus('project-1'), exact: false },
    ];
    const mutations: Array<(api: ManuscriptApiClient, queryClient: QueryClient) => Promise<void>> = [
      async (api, queryClient) => {
        const { result } = renderHook(() => useCreateImportQueueMutation(api), { wrapper: createWrapper(queryClient) });
        await act(() => result.current.mutateAsync({ projectId: 'project-1', body: createBody, idempotencyKey: 'idem-create' }));
      },
      async (api, queryClient) => {
        const { result } = renderHook(() => useUploadImportQueueItemMutation(api), { wrapper: createWrapper(queryClient) });
        await act(() => result.current.mutateAsync({
          projectId: 'project-1', queueId: 'queue-1', itemId: 'item-1',
          file: new File(['book'], 'book.fb2'), idempotencyKey: 'idem-upload',
        }));
      },
      async (api, queryClient) => {
        const { result } = renderHook(() => useSkipImportQueueItemMutation(api), { wrapper: createWrapper(queryClient) });
        await act(() => result.current.mutateAsync({ projectId: 'project-1', queueId: 'queue-1', itemId: 'item-1' }));
      },
      async (api, queryClient) => {
        const { result } = renderHook(() => useStartImportQueueMutation(api), { wrapper: createWrapper(queryClient) });
        await act(() => result.current.mutateAsync({
          projectId: 'project-1', queueId: 'queue-1', body: { expectedVersion: 2 }, idempotencyKey: 'idem-start',
        }));
      },
      async (api, queryClient) => {
        const { result } = renderHook(() => useCancelImportQueueMutation(api), { wrapper: createWrapper(queryClient) });
        await act(() => result.current.mutateAsync({ projectId: 'project-1', queueId: 'queue-1' }));
      },
    ];

    for (const mutate of mutations) {
      const queryClient = new QueryClient();
      const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue();
      const api = {
        cancelImportQueue: vi.fn(async () => queue),
        createImportQueue: vi.fn(async () => queue),
        startImportQueue: vi.fn(async () => queue),
        skipImportQueueItem: vi.fn(async () => queue),
        uploadImportQueueItem: vi.fn(async () => queue),
      } as unknown as ManuscriptApiClient;
      await mutate(api, queryClient);

      expect(invalidateQueries.mock.calls).toEqual(expectedInvalidations.map((invalidation) => [invalidation]));
    }
  });

  it('passes stable idempotency keys to create, upload and start requests', async () => {
    const queryClient = new QueryClient();
    const api = {
      createImportQueue: vi.fn(async () => queue),
      startImportQueue: vi.fn(async () => queue),
      uploadImportQueueItem: vi.fn(async () => queue),
    } as unknown as ManuscriptApiClient;
    const wrapper = createWrapper(queryClient);
    const file = new File(['book'], 'book.fb2');
    const created = renderHook(() => useCreateImportQueueMutation(api), { wrapper });
    await act(() => created.result.current.mutateAsync({ projectId: 'project-1', body: createBody, idempotencyKey: 'idem-create' }));
    const uploaded = renderHook(() => useUploadImportQueueItemMutation(api), { wrapper });
    await act(() => uploaded.result.current.mutateAsync({
      projectId: 'project-1', queueId: 'queue-1', itemId: 'item-1', file, idempotencyKey: 'idem-upload',
    }));
    const started = renderHook(() => useStartImportQueueMutation(api), { wrapper });
    await act(() => started.result.current.mutateAsync({
      projectId: 'project-1', queueId: 'queue-1', body: { expectedVersion: 2 }, idempotencyKey: 'idem-start',
    }));

    expect(api.createImportQueue).toHaveBeenCalledWith('project-1', createBody, { idempotencyKey: 'idem-create' });
    expect(api.uploadImportQueueItem).toHaveBeenCalledWith('project-1', 'queue-1', { itemId: 'item-1', file }, { idempotencyKey: 'idem-upload' });
    expect(api.startImportQueue).toHaveBeenCalledWith('project-1', 'queue-1', { expectedVersion: 2 }, { idempotencyKey: 'idem-start' });
  });
});
