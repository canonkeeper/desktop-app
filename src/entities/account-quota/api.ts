import { queryOptions, useQuery, type QueryClient } from '@tanstack/react-query';
import type { CanonKeeperApiClient, OperationResponse, Schema } from '../../shared/api';
import { isApiStatusFailure } from '../api-errors';
import { canonKeeperQueryKeys } from '../session/api';

/** The signed-in writer's own limits and use (getAccountQuota, quota spec R5.1). */
export type AccountQuota = OperationResponse<'getAccountQuota', '200'>;
export type QuotaCounter = Schema<'QuotaCounter'>;
export type AccountQuotaClient = Pick<CanonKeeperApiClient, 'getAccountQuota'>;

export const accountQuotaQueryKeys = {
  all: [...canonKeeperQueryKeys.all, 'account-quota'] as const,
};

function quotaApi(meta: unknown) {
  const api = (meta as { canonKeeperApi?: AccountQuotaClient } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('Account quota query is missing CanonKeeper API client metadata.');
  return api;
}

export function accountQuotaQueryOptions(api: AccountQuotaClient) {
  return queryOptions({
    queryKey: accountQuotaQueryKeys.all,
    queryFn: ({ meta, signal }) => quotaApi(meta).getAccountQuota({ signal }),
    meta: { canonKeeperApi: api },
    staleTime: 30_000,
  });
}

export function useAccountQuota(api: AccountQuotaClient, enabled = true) {
  return useQuery({ ...accountQuotaQueryOptions(api), enabled });
}

/** Re-reads the quota after an accepted or finished operation and after a refusal (R5.2). */
export function invalidateAccountQuota(queryClient: QueryClient) {
  return queryClient.invalidateQueries({ queryKey: accountQuotaQueryKeys.all, exact: true });
}

/** A hint is shown from 80 % of a limit (R5.3). */
export function quotaNearLimit(counter: QuotaCounter | undefined) {
  return Boolean(counter && counter.limit > 0 && counter.used * 5 >= counter.limit * 4);
}

/** The stable code of a quota refusal (quota spec R1.6), or '' for any other failure. */
export function quotaFailureCode(error: unknown) {
  if (!isApiStatusFailure(error)) return '';
  const code = error.problem.code ?? '';
  return code.startsWith('quota.') ? code : '';
}

export const supportAddress = 'support@canonkeeper.ru';
