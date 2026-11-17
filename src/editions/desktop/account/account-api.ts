// SPDX-License-Identifier: AGPL-3.0-only
import { queryOptions, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { invalidateAccountQuota } from '../../../entities/account-quota/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import type { CanonKeeperApiClient, OperationResponse } from '../../../shared/api';
import { desktopStateQueryKey } from '../settings/desktop-state';

export type DesktopAccount = OperationResponse<'getAccount', '200'>;
export type AccountInfo = NonNullable<DesktopAccount['account']>;

/** The operations of the link of the contract of the app (contracts/openapi.desktop.json; specs/2026-11-13-desk-account.md). */
export type AccountClient = Pick<CanonKeeperApiClient, 'getAccount' | 'startAccountLink' | 'cancelAccountLink' | 'unlinkAccount'>;

/** The one query of the link: the part «Аккаунт», the window of the waiting, the observer of its end and the lines read it. */
export const accountQueryKey = [...canonKeeperQueryKeys.all, 'desktop-account'] as const;

function clientOf(meta: unknown) {
  const api = (meta as { accountApi?: Pick<AccountClient, 'getAccount'> } | undefined)?.accountApi;
  if (!api) throw new Error('The link of the app is missing the CanonKeeper API client metadata.');
  return api;
}

/** How often the page asks again while the link waits for the confirmation in the browser (AW1.1: once in 1–2 seconds). */
export const waitingPollMs = 1_500;

/**
 * The link of the app with an account (getAccount; specs/2026-11-15-desk-account-link-web.md AW1, AW2): the page counts and
 * keeps nothing of it — while it waits the server is asked again until it says how the waiting ended.
 */
export function useDesktopAccount(api: Pick<AccountClient, 'getAccount'>, enabled = true) {
  return useQuery({
    ...accountQueryOptions(api),
    enabled,
    refetchInterval: (query) => (query.state.data?.state === 'waiting' ? waitingPollMs : false),
  });
}

/**
 * The link as the one query reads it: a refusal of a start reads it again by these (AW1.2). A failure of its read is its own
 * (`outageScope: 'local'`, specs/2026-11-17-desk-update-linux.md UL4.1): the part says it, the root does not probe for it.
 */
export function accountQueryOptions(api: Pick<AccountClient, 'getAccount'>) {
  return queryOptions({
    queryKey: accountQueryKey,
    queryFn: ({ meta, signal }) => clientOf(meta).getAccount({ signal }),
    meta: { accountApi: api, outageScope: 'local' },
  });
}

/**
 * What a link made or undone changes beyond the part (AW1.3, AW3.6): whether the helper is connected — the line of the reading
 * of every saga and the field of the chat read it —, the settings of the helper (who answers now), the counters of the tier
 * and where the app goes in the network.
 */
export function afterLinkChange(client: QueryClient) {
  void client.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'indexing-status'] });
  void client.invalidateQueries({ queryKey: [...canonKeeperQueryKeys.all, 'desktop-ai'] });
  void invalidateAccountQuota(client);
  void client.invalidateQueries({ queryKey: desktopStateQueryKey });
}

/** «Обновить сведения» (AW2.2): the site is asked now; the answer is what the part shows next. */
export function useRefreshAccount(api: AccountClient) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => api.getAccount({ fresh: true }),
    onSuccess: (account) => client.setQueryData(accountQueryKey, account),
  });
}

/** «Отвязать» (AW1.5): what the unlinking reached; the link and what follows it are read again. */
export function useUnlinkAccount(api: AccountClient) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => api.unlinkAccount(),
    onSettled: () => {
      void client.invalidateQueries({ queryKey: accountQueryKey });
      afterLinkChange(client);
    },
  });
}
