// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useMutationState, useQueryClient, type Mutation, type QueryClient } from '@tanstack/react-query';
import { sagaQueryKeys } from '../../../entities/saga/api';
import { createIdempotencyKey, type OperationRequest } from '../../../shared/api';
import { busyRetry, isClosedRun, obsidianKeys, problemCode, type ObsidianClient, type ObsidianExport } from './obsidian-api';

/** One export: the saga, the word of the author and the key of its repeat. */
export interface ExportWrite {
  projectId: string;
  body: OperationRequest<'exportObsidianSaga'>;
  idempotencyKey: string;
}

/** The state of an export as the cache of the mutations keeps it. */
export interface ExportState {
  id: number;
  write: ExportWrite;
  status: 'pending' | 'success' | 'error' | 'idle';
  answer: ObsidianExport | undefined;
  error: unknown;
  /** The tries that met `obsidian.busy` while it waits for the vault (the line of the run says so). */
  failures: number;
  failureReason: unknown;
  submittedAt: number;
}

/** A refusal of the map: its run leaves the result before it in place, the map leads the button (XW3.1). */
function refusedByTheMap(error: unknown) {
  return problemCode(error) === 'obsidian.map_not_ready';
}

/**
 * After an export, its result or its refusal (XW2.1, XW2.4, XW3.1, XW3.2): the folder of the saga is read again — its folder of
 * the export, the time of the last one, a vault gone or unlinked —, and a map that is not ready; the export takes its state once
 * they are read, so its words are those of the fresh folder. The results before it close — but for a refusal of the map, which
 * leaves the result before it standing.
 */
function afterExport(client: QueryClient, projectId: string, error: unknown) {
  if (!refusedByTheMap(error)) {
    // This export is still pending here: the ones before it, which ended, are dropped.
    const mutations = client.getMutationCache();
    for (const mutation of mutations.findAll({ mutationKey: obsidianKeys.export, exact: true })) {
      if ((mutation.state.variables as ExportWrite | undefined)?.projectId === projectId && mutation.state.status !== 'pending') mutations.remove(mutation);
    }
  }
  return Promise.all([
    client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) }),
    refusedByTheMap(error) ? client.invalidateQueries({ queryKey: sagaQueryKeys.map(projectId, 'published') }) : null,
  ]);
}

/**
 * The export of the map of a saga (specs/2026-11-16-desk-obsidian-export-web.md XW2.1): a mutation of the edition's key — a leave
 * of the page does not break it off, the result waits in the cache of the mutations while the window is open —, no signal: the
 * server writes on without the request. Only `obsidian.busy` waits, by its Retry-After. The button of the part and «Заменить»
 * start a new export with a new key; «Повторить» repeats the export with its key and its word.
 */
export function useExport(api: Pick<ObsidianClient, 'exportObsidianSaga'>) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationKey: obsidianKeys.export,
    gcTime: Infinity,
    mutationFn: (write: ExportWrite) => api.exportObsidianSaga(write.projectId, write.body, { idempotencyKey: write.idempotencyKey }),
    ...busyRetry,
    onSettled: (_answer, error, write) => afterExport(client, write.projectId, error),
  });

  function run(write: ExportWrite) {
    void mutation.mutateAsync(write).catch(() => undefined);
  }

  /** «Выгрузить в Obsidian», «Выгрузить снова», «Заменить»: a new export by a new key. */
  function start(projectId: string, replaceChanged = false) {
    run({ projectId, body: replaceChanged ? { replaceChanged: true } : {}, idempotencyKey: createIdempotencyKey('exportObsidianSaga') });
  }

  return { start, repeat: run };
}

function stateOf(mutation: Mutation): ExportState {
  return {
    id: mutation.mutationId,
    write: mutation.state.variables as ExportWrite,
    status: mutation.state.status,
    answer: mutation.state.data as ObsidianExport | undefined,
    error: mutation.state.error,
    failures: mutation.state.failureCount,
    failureReason: mutation.state.failureReason,
    submittedAt: mutation.state.submittedAt,
  };
}

/** Every export of this window (XW2.8), each with its saga. */
export function useAllExports() {
  return useMutationState({ filters: { mutationKey: obsidianKeys.export, exact: true }, select: stateOf }).filter((each) => each.write);
}

/**
 * The latest export of a saga (XW2.2): a page that comes back sees it going or its result — but not the result of an export the
 * unlink or a new path closed while it went (XW2.4), nor a refusal of the map, which leaves the result before it (XW3.1).
 */
export function useExportOf(projectId: string) {
  const exports = useMutationState({
    filters: {
      mutationKey: obsidianKeys.export, exact: true,
      predicate: (mutation) => (mutation.state.variables as ExportWrite | undefined)?.projectId === projectId && (mutation.state.status === 'pending'
        || (!isClosedRun(mutation) && !(mutation.state.status === 'error' && refusedByTheMap(mutation.state.error)))),
    },
    select: stateOf,
  });
  return [...exports].sort((left, right) => left.submittedAt - right.submittedAt).at(-1) ?? null;
}

/** An export of the saga goes now: the one fence of the bridge holds the vault (XW2.3). */
export function useExportGoing(projectId: string) {
  return useExportOf(projectId)?.status === 'pending';
}
