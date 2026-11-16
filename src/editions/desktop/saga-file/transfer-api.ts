// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery } from '@tanstack/react-query';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import type { CanonKeeperApiClient, Schema } from '../../../shared/api';

/** The one transfer of a saga from the site of this run of the app (specs/2026-11-16-desk-site-saga.md TR2.2). */
export type SiteSagaTransfer = Schema<'SiteSagaTransfer'>;
export type TransferClient = Pick<CanonKeeperApiClient, 'getSiteSagaTransfer' | 'startSiteSagaTransfer' | 'cancelSiteSagaTransfer'>;

/** The one query of the transfer (TR6.3): the window of the waiting, the line of its work, the entries and its end read it. */
export const siteTransferKey = [...canonKeeperQueryKeys.all, 'site-saga-transfer'] as const;

/** How often the transfer is read while it waits for the site, downloads or opens; otherwise it is not read again. */
export const transferPollMs = 1_000;

const goingPhases: ReadonlySet<SiteSagaTransfer['phase']> = new Set(['waiting', 'downloading', 'opening']);

/** The transfer downloads or opens: the backend starts no second one and stops none that opens (TR2.1, TR2.2). */
export function transferRuns(transfer: SiteSagaTransfer | undefined) {
  return transfer?.phase === 'downloading' || transfer?.phase === 'opening';
}

function transferApi(meta: unknown) {
  const api = (meta as { canonKeeperApi?: Pick<TransferClient, 'getSiteSagaTransfer'> } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('The transfer of a saga is missing the CanonKeeper API client metadata.');
  return api;
}

/**
 * The transfer as the backend holds it (TR6.3). One reader follows it — the edition around the pages: once a second while it
 * waits, downloads or opens, otherwise not at all; the answers of its start and its stop go into the same query. A failure of
 * its read is its own (`outageScope: 'local'`): the root does not take it for the author signed out.
 */
export function useSiteSagaTransfer(api: Pick<TransferClient, 'getSiteSagaTransfer'>) {
  return useQuery({
    queryKey: siteTransferKey,
    queryFn: ({ meta, signal }) => transferApi(meta).getSiteSagaTransfer({ signal }),
    meta: { canonKeeperApi: api, outageScope: 'local' },
    refetchInterval: (query) => (goingPhases.has(query.state.data?.phase as SiteSagaTransfer['phase']) ? transferPollMs : false),
  });
}
