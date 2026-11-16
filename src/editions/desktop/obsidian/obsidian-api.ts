// SPDX-License-Identifier: AGPL-3.0-only
import { queryOptions, useQuery, type Mutation, type QueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../../entities/api-errors';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { createIdempotencyKey, type CanonKeeperApiClient, type OperationRequest, type OperationResponse, type Schema } from '../../../shared/api';

/**
 * The reads of the bridge with an Obsidian vault (specs/2026-11-15-desk-obsidian-web.md OW7.1): React Query holds them. The
 * folder of the saga is an ordinary read. The look over the folder and the preview read the vault under the fence of the
 * saga — one reading a saga, its place held until the reading ends, even after the request went —, so they are never read
 * again by themselves: no repeat on the return to the window, on a mount or on a reconnect, a cache without term, no signal.
 * A repeat waits only for `obsidian.busy`, by its Retry-After: six tries in all.
 */
export type ObsidianClient = Pick<CanonKeeperApiClient,
  'getObsidianVault' | 'beginObsidianVaultChoice' | 'unlinkObsidianVault' | 'scanObsidianVault' | 'previewObsidianImport'
  | 'importObsidianMaterials' | 'importObsidianBooks' | 'linkBookToObsidianFolder' | 'unlinkBookFromObsidianFolder' | 'updateBookFromObsidian'
  | 'exportObsidianSaga'>;

export type ObsidianVault = OperationResponse<'getObsidianVault', '200'>;
export type ObsidianVaultBook = Schema<'ObsidianVaultBook'>;
export type ObsidianTree = OperationResponse<'scanObsidianVault', '200'>;
export type ObsidianTreeFolder = Schema<'ObsidianTreeFolder'>;
export type ObsidianPreviewAnswer = OperationResponse<'previewObsidianImport', '200'>;
export type ObsidianPreviewRequest = OperationRequest<'previewObsidianImport'>;
export type ObsidianBookPreview = Schema<'ObsidianBookPreview'>;
export type ObsidianRecordPreview = Schema<'ObsidianRecordPreview'>;
export type ObsidianMaterialsLoad = OperationResponse<'importObsidianMaterials', '200'>;
export type ObsidianBooksLoad = OperationResponse<'importObsidianBooks', '200'>;
/** The start of «Обновить из Obsidian»: the import Job of the update, and at a preview the fingerprint and the report of the folder. */
export type ObsidianBookUpdate = OperationResponse<'updateBookFromObsidian', '202'>;
/** The result of an export of the map of the saga into the vault (specs/2026-11-16-desk-obsidian-export-web.md XW2). */
export type ObsidianExport = OperationResponse<'exportObsidianSaga', '200'>;

/** Under the product root: a change of the author drops them. */
export const obsidianKeys = {
  all: ['canonkeeper', 'obsidian'] as const,
  saga: (projectId: string) => [...obsidianKeys.all, projectId] as const,
  vault: (projectId: string) => [...obsidianKeys.saga(projectId), 'vault'] as const,
  /** The reads of the vault of the saga: the unlink drops them. */
  reads: (projectId: string) => [...obsidianKeys.saga(projectId), 'reads'] as const,
  tree: (projectId: string) => [...obsidianKeys.reads(projectId), 'tree'] as const,
  preview: (projectId: string) => [...obsidianKeys.reads(projectId), 'preview'] as const,
  /** The two writes of «Загрузить» in the cache of the mutations: a page that comes back sees them. */
  loadMaterials: ['canonkeeper', 'obsidian', 'load', 'materials'] as const,
  loadBooks: ['canonkeeper', 'obsidian', 'load', 'books'] as const,
  /**
   * «Обновить книгу» of «Обновить из Obsidian» in the cache of the mutations (specs/2026-11-15-desk-obsidian-book-web.md OU3.2): the
   * server does not keep where an update came from, so the lines of its result know an update of this window from a folder by it.
   */
  update: ['canonkeeper', 'obsidian', 'update'] as const,
  /**
   * The export of the map of a saga in the cache of the mutations (specs/2026-11-16-desk-obsidian-export-web.md XW2.1, XW2.2): its
   * run and result outlive the page, a page that comes back and the line of its end on another page see them.
   */
  export: ['canonkeeper', 'obsidian', 'export'] as const,
};

export function problemCode(error: unknown): string | null {
  return isApiStatusFailure(error) ? error.problem.code ?? null : null;
}

/** A field of the problem of a refusal of the bridge (`folders`, `vaultReason`, `notes`…). */
export function problemField<T>(error: unknown, name: string): T | undefined {
  return isApiStatusFailure(error) ? (error.problem as Record<string, unknown>)[name] as T | undefined : undefined;
}

/** The seconds of Retry-After of `obsidian.busy`; the server says 5. */
export function retryAfterOf(error: unknown) {
  const header = (error as { response?: Response } | null)?.response?.headers?.get('Retry-After');
  const seconds = Number(header);
  return Number.isFinite(seconds) && seconds > 0 ? seconds : 5;
}

/** How many times a read waits for a vault read by another operation (OW2.8): six tries in all — the first and five repeats. */
export const busyRepeats = 5;

/**
 * Only `obsidian.busy` is repeated, by its Retry-After, five times after the first — «после шести попыток» of Data; `slow` and
 * the rest are said at once. React Query asks with the failures before this one: 0 after the first.
 */
export const busyRetry = {
  retry: (failures: number, error: unknown) => problemCode(error) === 'obsidian.busy' && failures < busyRepeats,
  retryDelay: (_failures: number, error: unknown) => retryAfterOf(error) * 1_000,
};

/** A read under the fence: never by itself again, kept without term (OW7.1). */
const fenced = {
  staleTime: Infinity,
  gcTime: Infinity,
  refetchOnWindowFocus: false,
  refetchOnMount: false,
  refetchOnReconnect: false,
  ...busyRetry,
} as const;

/** The client of a read rides in its meta: the key names only what is read (the rule of the queries of the product). */
function clientOf<T>(meta: unknown): T {
  const api = (meta as { obsidianApi?: T } | undefined)?.obsidianApi;
  if (!api) throw new Error('A read of the bridge is missing the client of the API.');
  return api;
}

/** The folder of the saga: an ordinary read (OW7.1). */
export function vaultQueryOptions(api: Pick<ObsidianClient, 'getObsidianVault'>, projectId: string) {
  return queryOptions({
    queryKey: obsidianKeys.vault(projectId),
    queryFn: ({ meta, signal }) => clientOf<Pick<ObsidianClient, 'getObsidianVault'>>(meta).getObsidianVault(projectId, { signal }),
    meta: { obsidianApi: api },
    enabled: projectId.length > 0,
  });
}

export function useObsidianVault(api: Pick<ObsidianClient, 'getObsidianVault'>, projectId: string, enabled = true) {
  return useQuery({ ...vaultQueryOptions(api, projectId), enabled: enabled && projectId.length > 0 });
}

/**
 * The look over the folder (OW2.8): read on the entry to the choice when there is none or it is old (`markTreeOld`), and by
 * «Прочитать папку заново»; never by itself — without a term, it is old only when the page said so.
 */
export function treeQueryOptions(api: Pick<ObsidianClient, 'scanObsidianVault'>, projectId: string) {
  return queryOptions({
    queryKey: obsidianKeys.tree(projectId),
    // No signal: a request broken off would not stop the reading on the server.
    queryFn: ({ meta }) => clientOf<Pick<ObsidianClient, 'scanObsidianVault'>>(meta).scanObsidianVault(projectId),
    meta: { obsidianApi: api },
    ...fenced,
    refetchOnMount: true,
  });
}

/**
 * The one rule of the look after «Связать», «Снять связь» and a write of the bridge (OW2.8; Sm4 of the review): no reading
 * starts — the word of the book of a folder and the closed variants come from `getObsidianVault`, which knows the live books —;
 * the look is old and is read on the next entry to the choice of the folders or by «Прочитать папку заново».
 */
export function markTreeOld(client: QueryClient, projectId: string) {
  void client.invalidateQueries({ queryKey: obsidianKeys.tree(projectId), refetchType: 'none' });
}

/**
 * The preview the page asked for (OW3.1, OW4.3): its request, the answer, the keys of its two writes — new for every
 * preview —, the live books of the saga when it was asked — another list since makes it old (OW3.4) — and whether an action
 * of the page since made it old for the books.
 */
export interface ObsidianPreview {
  id: string;
  request: ObsidianPreviewRequest;
  answer: ObsidianPreviewAnswer;
  keys: { materials: string; books: string };
  liveBooks: readonly string[];
  booksChanged: boolean;
}

/** The live books of a saga as a preview keeps them: their ids in one order. */
export function liveBookIds(books: ReadonlyArray<{ id: string }>) {
  return books.map((book) => book.id).sort();
}

/**
 * The choice the last press asked to preview, by saga. The read and its observer are one query with one function: React Query
 * gives the function of the observer to the repeats of a read, so the function finds what was asked here, not in the options.
 */
const asked = new Map<string, { request: ObsidianPreviewRequest; liveBooks: readonly string[] }>();
let previews = 0;

function previewQueryOptions(api: Pick<ObsidianClient, 'previewObsidianImport'>, projectId: string) {
  return queryOptions<ObsidianPreview>({
    queryKey: obsidianKeys.preview(projectId),
    queryFn: async ({ meta }) => {
      const press = asked.get(projectId);
      if (!press) throw new Error('The preview of the bridge is read only by a press.');
      const { request, liveBooks } = press;
      return {
        id: `preview-${(previews += 1)}`,
        request,
        liveBooks,
        answer: await clientOf<Pick<ObsidianClient, 'previewObsidianImport'>>(meta).previewObsidianImport(projectId, request),
        keys: { materials: createIdempotencyKey('importObsidianMaterials'), books: createIdempotencyKey('importObsidianBooks') },
        booksChanged: false,
      };
    },
    meta: { obsidianApi: api },
    ...fenced,
  });
}

/** The preview is read only by a press; this observes it — what goes on, what came — and never reads it itself. */
export function usePreview(api: Pick<ObsidianClient, 'previewObsidianImport'>, projectId: string) {
  return useQuery({ ...previewQueryOptions(api, projectId), enabled: false });
}

/**
 * «Показать, что получится», «Показать заново» (OW3.1): one read, kept in the cache past the page. A new preview closes the
 * result of the load of the one before (OW4.1).
 */
export function readPreview(
  client: QueryClient, api: Pick<ObsidianClient, 'previewObsidianImport'>, projectId: string, request: ObsidianPreviewRequest, liveBooks: readonly string[],
) {
  closeResults(client, projectId);
  asked.set(projectId, { request, liveBooks });
  return client.fetchQuery({ ...previewQueryOptions(api, projectId), staleTime: 0 });
}

/** An action of the page changed the books of the preview (OW3.4): «Книги изменились» until «Показать заново». */
export function markBooksChanged(client: QueryClient, projectId: string) {
  client.setQueryData<ObsidianPreview>(obsidianKeys.preview(projectId), (preview) => (preview ? { ...preview, booksChanged: true } : preview));
}

/**
 * The result of «Загрузить» of the saga is closed (OW4.1): «Загрузить ещё» or a new preview drops the writes that ended from the
 * cache of the mutations, where they wait while the window is open — but a write of books whose load of «Книги» is still live:
 * the observer of the edition reads the folder of the saga at its end by it (OW4.5).
 */
export function closeResults(client: QueryClient, projectId: string) {
  const mutations = client.getMutationCache();
  const queues = client.getQueryData<{ data?: Array<{ id: string; status: string }> }>(manuscriptQueryKeys.importQueues(projectId))?.data ?? [];
  const live = new Set(queues.filter((queue) => ['uploading', 'draft', 'running'].includes(queue.status)).map((queue) => queue.id));
  for (const mutation of mutations.getAll()) {
    const key = JSON.stringify(mutation.options.mutationKey);
    const of = (mutation.state.variables as { projectId?: string } | undefined)?.projectId;
    const queue = (mutation.state.data as { queue?: { id: string } | null } | undefined)?.queue?.id;
    const ours = key === JSON.stringify(obsidianKeys.loadMaterials) || key === JSON.stringify(obsidianKeys.loadBooks);
    if (ours && of === projectId && mutation.state.status !== 'pending' && !(queue && live.has(queue))) mutations.remove(mutation);
  }
}

/** The exports closed while they went: the unlink or a new path came during them; their result is not shown when they end. */
const closedRuns = new WeakSet<Mutation>();

/** An export closed while it went (XW2.4): it fences the vault while it goes, its result is not shown. */
export function isClosedRun(mutation: Mutation) {
  return closedRuns.has(mutation);
}

/**
 * The result of the export of the saga is closed (XW2.4): the unlink of the folder and a change of its path drop the exports of
 * the saga that ended from the cache of the mutations, where they wait while the window is open, and close the one that goes —
 * its result, of the folder before, is not shown when it ends. A new export closes the ones before it when it ends.
 */
export function closeExports(client: QueryClient, projectId: string) {
  const mutations = client.getMutationCache();
  for (const mutation of mutations.findAll({ mutationKey: obsidianKeys.export, exact: true })) {
    const of = (mutation.state.variables as { projectId?: string } | undefined)?.projectId;
    if (of !== projectId) continue;
    if (mutation.state.status === 'pending') closedRuns.add(mutation);
    else mutations.remove(mutation);
  }
}

/** The unlink of the folder drops the reads of the bridge of the saga (OW1.6). */
export function dropReads(client: QueryClient, projectId: string) {
  client.removeQueries({ queryKey: obsidianKeys.reads(projectId) });
}
