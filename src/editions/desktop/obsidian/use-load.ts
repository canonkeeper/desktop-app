// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useMutationState, useQueryClient, type Mutation, type QueryClient } from '@tanstack/react-query';
import { authorMaterialQueryKeys } from '../../../entities/author-materials/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { sagaQueryKeys } from '../../../entities/saga/api';
import { createIdempotencyKey, type OperationRequest } from '../../../shared/api';
import { loadPlan, type Decisions } from './load';
import {
  busyRetry, markTreeOld, obsidianKeys, problemCode,
  type ObsidianBooksLoad, type ObsidianClient, type ObsidianMaterialsLoad, type ObsidianPreview,
} from './obsidian-api';

/** One write of «Загрузить»: the saga, the preview it loads and the run of both writes, its body and its key of a repeat. */
export interface LoadWrite<Body> {
  projectId: string;
  previewId: string;
  run: string;
  /** Whether the run has the other write too (the line on another page says what each part did, OW4.1). */
  parts: { materials: boolean; books: boolean };
  /** Titles of the folders of the preview, for the line on another page. */
  body: Body;
  idempotencyKey: string;
}

export type MaterialsWrite = LoadWrite<OperationRequest<'importObsidianMaterials'>>;
export type BooksWrite = LoadWrite<OperationRequest<'importObsidianBooks'>>;

/** After a write of records: «Ваши материалы» and the map are read again, the look is old (OW4.5, OW2.8). */
function afterRecords(client: QueryClient, projectId: string) {
  void client.invalidateQueries({ queryKey: authorMaterialQueryKeys.project(projectId) });
  void client.invalidateQueries({ queryKey: sagaQueryKeys.projectMaps(projectId) });
  markTreeOld(client, projectId);
}

/**
 * After a write of books: the loads of «Книги» — whose poll runs only while one is live —, the books and the folder of the
 * saga are read again; the look over the folder is old — read anew when the author comes to the choice (OW4.5, OW2.8).
 */
function afterBooks(client: QueryClient, projectId: string) {
  void client.invalidateQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId) });
  void client.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId }) });
  void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
  markTreeOld(client, projectId);
}

let runs = 0;

/**
 * The two writes of «Загрузить» (specs/2026-11-15-desk-obsidian-web.md OW4.1, OW4.3): mutations of the edition's keys — a leave
 * of the page does not break them off, the result waits in the cache of the mutations —, the records first, then the books;
 * each part apart: a refusal of one does not cancel the other. Each write goes with the key of its preview; a repeat after a
 * lost connection goes with the same key; `conflict` of the records is repeated once with the same key by itself, `busy` waits
 * by its Retry-After.
 */
export function useLoad(api: Pick<ObsidianClient, 'importObsidianMaterials' | 'importObsidianBooks'>) {
  const client = useQueryClient();
  const materials = useMutation({
    mutationKey: obsidianKeys.loadMaterials,
    // The result waits while the window is open, until «Загрузить ещё» or a new preview closes it (OW4.1).
    gcTime: Infinity,
    mutationFn: (write: MaterialsWrite) => api.importObsidianMaterials(write.projectId, write.body, { idempotencyKey: write.idempotencyKey }),
    retry: (failures, error) => (problemCode(error) === 'conflict' ? failures < 1 : busyRetry.retry(failures, error)),
    retryDelay: (failures, error) => (problemCode(error) === 'conflict' ? 0 : busyRetry.retryDelay(failures, error)),
    onSuccess: (_answer, write) => afterRecords(client, write.projectId),
  });
  const books = useMutation({
    mutationKey: obsidianKeys.loadBooks,
    gcTime: Infinity,
    mutationFn: (write: BooksWrite) => api.importObsidianBooks(write.projectId, write.body, { idempotencyKey: write.idempotencyKey }),
    ...busyRetry,
    onSettled: (_answer, _error, write) => afterBooks(client, write.projectId),
  });

  /** «Загрузить»: the records, then the books, each by its own key; one part that did not pass does not stop the other. */
  async function load(projectId: string, preview: ObsidianPreview, decisions: Decisions, queueLive: boolean) {
    const plan = loadPlan(preview, decisions, queueLive);
    const run = `run-${(runs += 1)}`;
    const parts = { materials: plan.materials !== null, books: plan.books !== null };
    const base = { projectId, previewId: preview.id, run, parts };
    if (plan.materials) await materials.mutateAsync({ ...base, body: plan.materials, idempotencyKey: preview.keys.materials }).catch(() => undefined);
    if (plan.books) await books.mutateAsync({ ...base, body: plan.books, idempotencyKey: preview.keys.books }).catch(() => undefined);
  }

  /** A repeat of a write after a lost connection or `conflict` said: the same key (OW4.3). */
  function repeatMaterials(write: MaterialsWrite) {
    void materials.mutateAsync(write).catch(() => undefined);
  }

  /** A repeat of the books after a lost connection: the same key — the server answers the load it began (OW4.3). */
  function repeatBooks(write: BooksWrite) {
    void books.mutateAsync(write).catch(() => undefined);
  }

  /** «Загрузить книги ещё раз» after `load_interrupted` or a failure after the load began: a new key, kept by the preview. */
  function loadBooksAgain(write: BooksWrite) {
    const idempotencyKey = createIdempotencyKey('importObsidianBooks');
    client.setQueryData<ObsidianPreview>(obsidianKeys.preview(write.projectId), (preview) => (
      preview && preview.id === write.previewId ? { ...preview, keys: { ...preview.keys, books: idempotencyKey } } : preview));
    void books.mutateAsync({ ...write, idempotencyKey }).catch(() => undefined);
  }

  return { load, repeatMaterials, repeatBooks, loadBooksAgain };
}

/** The state of a write as the cache of the mutations keeps it. */
export interface WriteState<Write, Answer> {
  write: Write;
  status: 'pending' | 'success' | 'error' | 'idle';
  answer: Answer | undefined;
  error: unknown;
  submittedAt: number;
}

function stateOf<Write, Answer>(mutation: Mutation): WriteState<Write, Answer> {
  return {
    write: mutation.state.variables as Write,
    status: mutation.state.status,
    answer: mutation.state.data as Answer | undefined,
    error: mutation.state.error,
    submittedAt: mutation.state.submittedAt,
  };
}

/** The writes of «Загрузить» of a preview, the latest of each part (OW4.1): a page that comes back sees them. */
export function useLoadOf(previewId: string | undefined) {
  const of = (mutation: Mutation) => (mutation.state.variables as LoadWrite<unknown> | undefined)?.previewId === previewId;
  const materials = useMutationState({ filters: { mutationKey: obsidianKeys.loadMaterials, predicate: of }, select: (mutation) => stateOf<MaterialsWrite, ObsidianMaterialsLoad>(mutation) });
  const books = useMutationState({ filters: { mutationKey: obsidianKeys.loadBooks, predicate: of }, select: (mutation) => stateOf<BooksWrite, ObsidianBooksLoad>(mutation) });
  const last = <T extends { submittedAt: number }>(list: readonly T[]) => [...list].sort((left, right) => left.submittedAt - right.submittedAt).at(-1) ?? null;
  return { materials: previewId ? last(materials) : null, books: previewId ? last(books) : null };
}

/** Every write of «Загрузить» of this window, for the line on another page (OW4.1). */
export function useAllLoads() {
  const materials = useMutationState({ filters: { mutationKey: obsidianKeys.loadMaterials }, select: (mutation) => ({ id: mutation.mutationId, ...stateOf<MaterialsWrite, ObsidianMaterialsLoad>(mutation) }) });
  const books = useMutationState({ filters: { mutationKey: obsidianKeys.loadBooks }, select: (mutation) => ({ id: mutation.mutationId, ...stateOf<BooksWrite, ObsidianBooksLoad>(mutation) }) });
  return { materials, books };
}
