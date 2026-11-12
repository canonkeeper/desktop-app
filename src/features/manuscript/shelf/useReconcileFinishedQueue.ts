import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { invalidateAccountQuota } from '../../../entities/account-quota/api';
import { manuscriptQueryKeys, type ImportQueue } from '../../../entities/manuscript/api';

/**
 * When the newest upload of the saga ends — succeeded, failed or stopped — the shelf reads again what it changed: the
 * quota, the Jobs of its volumes, the preparation of the saga and the shelf itself, once for each version of the queue.
 */
export function useReconcileFinishedQueue({ projectId, queue, requestedProjectId }: {
  projectId: string | null;
  queue: ImportQueue | null;
  requestedProjectId: string | null;
}) {
  const queryClient = useQueryClient();
  const reconciled = useRef(new Set<string>());
  useEffect(() => {
    if (!projectId || !queue) return;
    const key = `${queue.id}:${queue.version}`;
    if (reconciled.current.has(key)) return;
    reconciled.current.add(key);
    const jobIds = new Set(queue.items.map((item) => item.importJobId)
      .filter((jobId): jobId is string => Boolean(jobId)));
    void Promise.all([
      invalidateAccountQuota(queryClient),
      ...[...jobIds].map((jobId) => queryClient.invalidateQueries({
        queryKey: manuscriptQueryKeys.job(jobId),
        exact: true,
      })),
      queryClient.invalidateQueries({
        queryKey: manuscriptQueryKeys.indexingStatus(projectId),
        exact: false,
      }),
      // This route reads the shelf by `?projectId`, or without it for the default saga.
      ...[...new Set([projectId, requestedProjectId])].map((shelfProjectId) => queryClient.invalidateQueries({
        queryKey: manuscriptQueryKeys.shelf({ projectId: shelfProjectId }),
        exact: true,
      })),
    ]);
  }, [projectId, queryClient, queue, requestedProjectId]);
}
