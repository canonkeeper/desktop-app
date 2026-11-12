import { useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { invalidateAccountQuota } from '../../../entities/account-quota/api';
import { isApiStatusFailure, isTransientApiFailure } from '../../../entities/api-errors';
import { applyCoverBookResponse } from '../../../entities/manuscript/api/covers';
import {
  createFirstProjectWorkspace,
  createIdempotencyKey,
  manuscriptQueryKeys,
  type BookExportFormat,
  type CreateProjectInput,
  type ImportQueue,
  type ManuscriptApiClient,
  type RestoredSource,
  type SearchScope,
  type SourceView,
} from '../../../entities/manuscript/api/queries';
import type { ManuscriptShelfWorkspace } from '../../../entities/manuscript/api/queries';

type ImportQueueListClient = Pick<ManuscriptApiClient, 'listImportQueues'>;
type ImportQueueList = Awaited<ReturnType<ImportQueueListClient['listImportQueues']>>;
type RestoreDeletedSourceClient = Pick<ManuscriptApiClient, 'restoreBook' | 'restoreChapter' | 'restoreProject'>;
type RevertBookUpdateClient = Pick<ManuscriptApiClient, 'revertBookUpdate'>;
type CreateBookClient = Pick<ManuscriptApiClient, 'createBook'>;
type UpdateBookClient = Pick<ManuscriptApiClient, 'updateBook'>;
type DeleteBookClient = Pick<ManuscriptApiClient, 'deleteBook'>;
type UploadBookCoverClient = Pick<ManuscriptApiClient, 'uploadBookCover'>;
type DeleteBookCoverClient = Pick<ManuscriptApiClient, 'deleteBookCover'>;
type BookExportClient = Pick<ManuscriptApiClient, 'getBookExport'>;
type CreateImportQueueClient = Pick<ManuscriptApiClient, 'createImportQueue'>;
type UploadImportQueueItemClient = Pick<ManuscriptApiClient, 'uploadImportQueueItem'>;
type SkipImportQueueItemClient = Pick<ManuscriptApiClient, 'skipImportQueueItem'>;
type StartImportQueueClient = Pick<ManuscriptApiClient, 'startImportQueue'>;
type CancelImportQueueClient = Pick<ManuscriptApiClient, 'cancelImportQueue' | 'getImportQueue'>;
type CancelJobClient = Pick<ManuscriptApiClient, 'cancelJob' | 'getJob'>;
type SearchProjectClient = Pick<ManuscriptApiClient, 'searchProject'>;
type RestartIndexingClient = Pick<ManuscriptApiClient, 'startProjectIndexing'>;
type GetProjectClient = Pick<ManuscriptApiClient, 'getProject'>;
type ImportBookFileClient = Pick<ManuscriptApiClient, 'importBookFile'>;

async function applyImportQueueResponse(
  queryClient: QueryClient,
  projectId: string,
  queue: ImportQueue,
) {
  let stale = false;
  const exactKey = manuscriptQueryKeys.importQueue(projectId, queue.id);
  queryClient.setQueryData<ImportQueue>(exactKey, (current) => {
    if (current && current.version > queue.version) {
      stale = true;
      return current;
    }
    return queue;
  });
  queryClient.setQueryData<ImportQueueList>(manuscriptQueryKeys.importQueues(projectId), (current) => {
    if (!current) return current;
    const existing = current.data.find((item) => item.id === queue.id);
    if (existing && existing.version > queue.version) {
      stale = true;
      return current;
    }
    return {
      ...current,
      data: existing
        ? current.data.map((item) => item.id === queue.id ? queue : item)
        : [queue, ...current.data],
    };
  });

  const jobIds = new Set(queue.items.map((item) => item.importJobId)
    .filter((jobId): jobId is string => Boolean(jobId)));
  const invalidations = [
    invalidateAccountQuota(queryClient),
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId }), exact: true }),
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId), exact: false }),
    ...[...jobIds].map((jobId) => queryClient.invalidateQueries({
      queryKey: manuscriptQueryKeys.job(jobId),
      exact: true,
    })),
  ];
  if (stale) {
    invalidations.push(
      queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true }),
      queryClient.invalidateQueries({ queryKey: exactKey, exact: true }),
    );
  }
  await Promise.all(invalidations);
}

export function useRestoreDeletedSourceMutation(api: RestoreDeletedSourceClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { id: string; kind: RestoredSource['kind'] }): Promise<RestoredSource> => {
      switch (input.kind) {
        case 'project':
          return { kind: input.kind, resource: await api.restoreProject(input.id) };
        case 'book':
          return { kind: input.kind, resource: await api.restoreBook(input.id) };
        case 'chapter':
          return { kind: input.kind, resource: await api.restoreChapter(input.id) };
      }
    },
    onSuccess: () => Promise.all([
      queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all }),
      invalidateAccountQuota(queryClient),
    ]),
  });
}

/**
 * «Вернуть версию до обновления» (specs/2026-11-12-ux10-book-revert.md BR2.2) with the fingerprint of the preview the author
 * read: the shelf, «Недавно удалённые», the reading of the saga and the counters of the tier are read again.
 */
export function useRevertBookUpdateMutation(api: RevertBookUpdateClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, fingerprint }: { bookId: string; fingerprint: string }) => api.revertBookUpdate(bookId, { fingerprint }),
    onSettled: () => Promise.all([
      queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all }),
      invalidateAccountQuota(queryClient),
    ]),
  });
}

export function useCreateBookMutation(api: CreateBookClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ projectId, body }: { body: Parameters<CreateBookClient['createBook']>[1]; projectId: string }) =>
      api.createBook(projectId, body),
    onSuccess: (created, input) => {
      const addCreatedBook = (shelf: ManuscriptShelfWorkspace) => ({
        ...shelf,
        books: [...shelf.books, created],
      });
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId: input.projectId }),
        (shelf) => shelf && addCreatedBook(shelf),
      );
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId: undefined }),
        (shelf) => shelf?.project?.id === input.projectId ? addCreatedBook(shelf) : shelf,
      );
    },
  });
}

export function useUpdateBookMutation(api: UpdateBookClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, body }: { body: Parameters<UpdateBookClient['updateBook']>[1]; bookId: string }) =>
      api.updateBook(bookId, body),
    onSuccess: (updated) => {
      const updateBook = (shelf: ManuscriptShelfWorkspace) => ({
        ...shelf,
        books: shelf.books.map((book) => book.id === updated.id ? updated : book),
      });
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId: updated.projectId }),
        (shelf) => shelf && updateBook(shelf),
      );
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId: undefined }),
        (shelf) => shelf?.project?.id === updated.projectId ? updateBook(shelf) : shelf,
      );
    },
  });
}

/**
 * A move of a volume (specs/2026-11-11-ux7-reorder.md RK2.1): updateBook with the book to stand after, null — the start.
 * The server puts every book of the saga at its place, so the shelf, the reader and the status are read again — «Книга N»
 * is the place on the shelf; the keys of the chapters do not change.
 */
export function useMoveBookMutation(api: UpdateBookClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, afterBookId }: { afterBookId: string | null; bookId: string }) => api.updateBook(bookId, { afterBookId }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all }),
  });
}

export function useDeleteBookMutation(api: DeleteBookClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId }: { bookId: string; projectId: string }) => api.deleteBook(bookId),
    onSuccess: (_response, input) => {
      const queryKey = manuscriptQueryKeys.shelf({ projectId: input.projectId });
      // The Jobs stay the server's: the deletion stops the work on the book, and the refetch shows it stopping
      // (specs/2026-11-08-deletion-stops-processing.md R3.4).
      queryClient.setQueryData<ManuscriptShelfWorkspace>(queryKey, (shelf) => shelf && {
        ...shelf,
        books: shelf.books.filter((book) => book.id !== input.bookId),
      });
      return Promise.all([queryClient.invalidateQueries({ queryKey, exact: true }), invalidateAccountQuota(queryClient)]);
    },
  });
}

/**
 * Rewrites an existing book from a new file of the same volume, or with `preview` only tells what that would change
 * (specs/2026-10-27-update-book-from-file.md, UC1, UC6).
 */
export function useUpdateBookFromFileMutation(api: ImportBookFileClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, file, preview, projectId }: { bookId: string; file: File; preview: boolean; projectId: string }) => api.importBookFile(
      projectId,
      { file, metadata: { sourceFileName: file.name }, options: { importMode: preview ? 'update_preview' : 'update_book', bookId } },
      { idempotencyKey: createIdempotencyKey('importBookFile') },
    ),
    onSuccess: (response, input) => {
      queryClient.setQueryData(manuscriptQueryKeys.job(response.job.id), response.job);
      // Every shelf the browser reads takes the update in work: the shell follows it to its end on any page
      // (specs/2026-11-12-ux10-book-revert.md BR3.4), whatever saga its address names.
      return input.preview
        ? queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: input.projectId }), exact: true })
        : queryClient.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'shelf'] });
    },
    onSettled: () => invalidateAccountQuota(queryClient),
  });
}

export function useUploadBookCoverMutation(api: UploadBookCoverClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, file, onUploadProgress }: {
      bookId: string;
      file: File;
      onUploadProgress?: NonNullable<Parameters<UploadBookCoverClient['uploadBookCover']>[2]>['onUploadProgress'];
    }) => api.uploadBookCover(bookId, file, { onUploadProgress }),
    onSuccess: (updated) => applyCoverBookResponse(queryClient, updated),
  });
}

export function useDeleteBookCoverMutation(api: DeleteBookCoverClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (bookId: string) => api.deleteBookCover(bookId),
    onSuccess: (updated) => applyCoverBookResponse(queryClient, updated),
  });
}

/** «Править главу» (A8.7): the one draft of the chapter; the page of «Письмо» reads it then. */
export function useOpenChapterEditMutation(api: Pick<ManuscriptApiClient, 'openChapterEdit'>) {
  return useMutation({
    mutationFn: ({ projectId, chapterId }: { projectId: string; chapterId: string }) => api.openChapterEdit(projectId, chapterId),
  });
}

/** The file of a book, with its drawings when asked (BP3.1): not a state of the server, so nothing is kept in the cache. */
export function useBookExportMutation(api: BookExportClient) {
  return useMutation({
    mutationFn: ({ bookId, format, drawings = false }: { bookId: string; format: BookExportFormat; drawings?: boolean }) =>
      (drawings ? api.getBookExport(bookId, format, { drawings }) : api.getBookExport(bookId, format)),
  });
}

export function useCreateImportQueueMutation(api: CreateImportQueueClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      body,
      idempotencyKey,
      projectId,
    }: {
      body: Parameters<CreateImportQueueClient['createImportQueue']>[1];
      idempotencyKey: string;
      projectId: string;
    }) => api.createImportQueue(projectId, body, { idempotencyKey }),
    onSuccess: (queue, input) => applyImportQueueResponse(queryClient, input.projectId, queue),
    // A queue whose books would not fit is refused here, as its start is (writer-quotas R3.2).
    onError: () => invalidateAccountQuota(queryClient),
  });
}

export function useUploadImportQueueItemMutation(api: UploadImportQueueItemClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      file,
      idempotencyKey,
      itemId,
      onUploadProgress,
      projectId,
      queueId,
      signal,
    }: {
      file: File;
      idempotencyKey: string;
      itemId: string;
      onUploadProgress?: NonNullable<Parameters<UploadImportQueueItemClient['uploadImportQueueItem']>[3]>['onUploadProgress'];
      projectId: string;
      queueId: string;
      signal?: AbortSignal;
    }) => api.uploadImportQueueItem(projectId, queueId, { file, itemId }, { idempotencyKey, onUploadProgress, signal }),
    onSuccess: (queue, input) => applyImportQueueResponse(queryClient, input.projectId, queue),
  });
}

/** «Пропустить» of a volume that did not read: the queue of the server answers with the volume out (UP3.2). */
export function useSkipImportQueueItemMutation(api: SkipImportQueueItemClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ itemId, projectId, queueId }: { itemId: string; projectId: string; queueId: string }) =>
      api.skipImportQueueItem(projectId, queueId, itemId),
    onSuccess: (queue, input) => applyImportQueueResponse(queryClient, input.projectId, queue),
  });
}

export function useStartImportQueueMutation(api: StartImportQueueClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      body,
      idempotencyKey,
      projectId,
      queueId,
    }: {
      body: Parameters<StartImportQueueClient['startImportQueue']>[2];
      idempotencyKey: string;
      projectId: string;
      queueId: string;
    }) => api.startImportQueue(projectId, queueId, body, { idempotencyKey }),
    onSuccess: (queue, input) => applyImportQueueResponse(queryClient, input.projectId, queue),
    onError: () => invalidateAccountQuota(queryClient),
  });
}

export function useCancelImportQueueMutation(api: CancelImportQueueClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ projectId, queueId }: { projectId: string; queueId: string }) => {
      try {
        return await api.cancelImportQueue(projectId, queueId);
      } catch (error) {
        if (!isTransientApiFailure(error) && !(isApiStatusFailure(error) && error.status === 409)) throw error;
        return api.getImportQueue(projectId, queueId);
      }
    },
    onSuccess: (queue, input) => applyImportQueueResponse(queryClient, input.projectId, queue),
  });
}

export function useCancelJobMutation(api: CancelJobClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (jobId: string) => {
      const canceled = await api.cancelJob(jobId);
      if (!canceled) return undefined;
      return api.getJob(canceled.id);
    },
    onSuccess: async (job) => {
      if (!job) return;
      queryClient.setQueryData(manuscriptQueryKeys.job(job.id), job);
      const projectId = job.subject.projectId;
      const updateJob = (shelf: ManuscriptShelfWorkspace) => ({
        ...shelf,
        jobs: shelf.jobs.map((current) => current.id === job.id ? job : current),
      });
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId }),
        (shelf) => shelf && updateJob(shelf),
      );
      queryClient.setQueryData<ManuscriptShelfWorkspace>(
        manuscriptQueryKeys.shelf({ projectId: undefined }),
        (shelf) => shelf?.project?.id === projectId ? updateJob(shelf) : shelf,
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId }), exact: true }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId), exact: false }),
      ]);
    },
  });
}

export function useSearchProjectMutation(api: SearchProjectClient) {
  return useMutation({
    mutationFn: ({ projectId, query, sourceView, scope, filters }: {
      projectId: string;
      query: string;
      sourceView: SourceView;
      scope?: SearchScope;
      filters?: NonNullable<Parameters<SearchProjectClient['searchProject']>[4]>['filters'];
    }) => api.searchProject(projectId, query, sourceView, scope, filters ? { filters } : undefined),
  });
}

// Restarts the text index of one text version; its success queues the cards still missing.
export function useRestartIndexingMutation(api: RestartIndexingClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ projectId, sourceView }: { projectId: string; sourceView: SourceView }) =>
      api.startProjectIndexing(
        projectId,
        { sourceView },
        { idempotencyKey: createIdempotencyKey('startProjectIndexing') },
      ),
    onSuccess: async (_response, input) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(input.projectId), exact: false }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: input.projectId }), exact: true }),
      ]);
    },
  });
}

export function useGetProjectMutation(api: GetProjectClient) {
  return useMutation({
    mutationFn: (projectId: string) => api.getProject(projectId),
  });
}

export function useCreateFirstProjectWorkspaceMutation(api: Parameters<typeof createFirstProjectWorkspace>[0]) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateProjectInput) => createFirstProjectWorkspace(api, body),
    onSettled: () => invalidateAccountQuota(queryClient),
  });
}
