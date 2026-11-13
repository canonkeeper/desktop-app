// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery } from '@tanstack/react-query';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import type { CanonKeeperApiClient, OperationResponse } from '../../../shared/api';

export type DesktopState = OperationResponse<'getDesktopState', '200'>;
type DesktopStateClient = Pick<CanonKeeperApiClient, 'getDesktopState'>;

const desktopStateQueryKey = [...canonKeeperQueryKeys.all, 'desktop-state'] as const;

function stateApi(meta: unknown) {
  const api = (meta as { canonKeeperApi?: DesktopStateClient } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('The state of the app is missing the CanonKeeper API client metadata.');
  return api;
}

/** How often the page asks again while the server counts the place of the data (ST2.2). */
const countingPollMs = 1_000;

/**
 * The state of the app «Данные» and «О программе» show (specs/2026-11-13-desk-settings.md ST2.1): one read of the server;
 * while it counts the place of the data the page asks again until the count ended.
 */
export function useDesktopState(api: DesktopStateClient) {
  return useQuery({
    queryKey: desktopStateQueryKey,
    queryFn: ({ meta, signal }) => stateApi(meta).getDesktopState({ signal }),
    meta: { canonKeeperApi: api },
    refetchInterval: (query) => (query.state.data?.usage.counting ? countingPollMs : false),
  });
}

const dayFormatter = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });

/** «12 ноября 2026»: the day of a copy on the computer of the author, without «г.». */
export function dayWords(at: string) {
  const parts = dayFormatter.formatToParts(new Date(at));
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((each) => each.type === type)?.value ?? '';
  return `${part('day')} ${part('month')} ${part('year')}`;
}
