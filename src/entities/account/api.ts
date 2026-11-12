import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { canonKeeperQueryKeys, setSessionUser } from '../session/api';
import type { CanonKeeperApiClient } from '../../shared/api';

/** The operations of «Аккаунт» (specs/2026-11-10-ux2-tariff-account.md UC5). */
export type AccountPageClient = Pick<CanonKeeperApiClient, 'setAccountName' | 'getAccountSessions' | 'logoutOtherSessions'>;

/** Under the identity: a change of the author drops it. */
const sessionsKey = [...canonKeeperQueryKeys.all, 'account-sessions'] as const;

function pageApi(meta: unknown) {
  const api = (meta as { canonKeeperApi?: AccountPageClient } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('Account sessions query is missing CanonKeeper API client metadata.');
  return api;
}

/** «Вы вошли на N устройствах» (TA5.2): only the number. */
export function useAccountSessions(api: AccountPageClient) {
  return useQuery(queryOptions({
    queryKey: sessionsKey,
    queryFn: ({ meta, signal }) => pageApi(meta).getAccountSessions({ signal }),
    meta: { canonKeeperApi: api },
    staleTime: 30_000,
  }));
}

/** «Выйти на остальных» (TA5.3): the other sessions end, the count is read again. */
export function useLogoutOtherSessions(api: AccountPageClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.logoutOtherSessions(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionsKey, exact: true }),
  });
}

/** The name of the author (TA5.1): the answer names who is signed in, and the cache takes it. */
export function useSetAccountName(api: AccountPageClient) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (displayName: string) => api.setAccountName({ displayName }),
    onSuccess: (session) => setSessionUser(queryClient, session.user),
  });
}
