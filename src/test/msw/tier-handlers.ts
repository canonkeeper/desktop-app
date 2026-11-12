import { http } from 'msw';
import type { MockRepository } from '../../api/mock/repository';
import type { OperationId, OperationRequest, Schema } from '../../shared/api';
import { readJson } from './handler-input';
import { binaryResponse, jsonResponse, mswPath, oneParam, type MswCsrfState } from './handler-runtime';

/** The tiers of the site: «Тарифы», the writer's tier and the administrator's tab (specs/2026-11-08-subscription-tiers.md). */
export function createTierHandlers(repository: MockRepository, csrfState: MswCsrfState) {
  const security = (operationId: OperationId, request: Request) => ({ operationId, request, repository, csrfState });
  const body = async <TOperation extends OperationId>(request: Request) => (await readJson(request)) as unknown as OperationRequest<TOperation>;
  return [
    http.get(mswPath('listSubscriptionTiers'), ({ request }) => jsonResponse(
      () => repository.listSubscriptionTiers(), { headers: { 'Cache-Control': 'no-cache' } }, security('listSubscriptionTiers', request),
    )),
    http.get(mswPath('getAccountPlan'), ({ request }) => jsonResponse(() => repository.getAccountPlan(), undefined, security('getAccountPlan', request))),
    http.get(mswPath('listAdminSubscriptionTiers'), ({ request }) => jsonResponse(
      () => repository.listAdminSubscriptionTiers(), undefined, security('listAdminSubscriptionTiers', request),
    )),
    http.put(mswPath('setAdminSubscriptionTier'), ({ params, request }) => jsonResponse(
      async () => repository.setAdminSubscriptionTier(oneParam(params.tier) as Schema<'SubscriptionTierCode'>, await body<'setAdminSubscriptionTier'>(request)),
      undefined, security('setAdminSubscriptionTier', request),
    )),
    http.post(mswPath('grantAdminWriterTier'), ({ params, request }) => jsonResponse(
      async () => repository.grantAdminWriterTier(oneParam(params.userId), await body<'grantAdminWriterTier'>(request)), { status: 201 },
      security('grantAdminWriterTier', request),
    )),
    http.post(mswPath('endAdminWriterPeriod'), ({ params, request }) => jsonResponse(
      () => repository.endAdminWriterPeriod(oneParam(params.userId), oneParam(params.periodId)), undefined, security('endAdminWriterPeriod', request),
    )),
    // The keys (TR4, TR5): the file of a batch is the one answer with its keys.
    http.post(mswPath('checkSubscriptionKey'), ({ request }) => jsonResponse(
      async () => repository.checkSubscriptionKey(await body<'checkSubscriptionKey'>(request)), undefined, security('checkSubscriptionKey', request),
    )),
    http.post(mswPath('redeemSubscriptionKey'), ({ request }) => jsonResponse(
      async () => repository.redeemSubscriptionKey(await body<'redeemSubscriptionKey'>(request)), undefined, security('redeemSubscriptionKey', request),
    )),
    http.get(mswPath('listAdminKeyBatches'), ({ request }) => jsonResponse(
      () => repository.listAdminKeyBatches(), undefined, security('listAdminKeyBatches', request),
    )),
    http.post(mswPath('createAdminKeyBatch'), ({ request }) => binaryResponse(async () => {
      const file = await repository.createAdminKeyBatch(await body<'createAdminKeyBatch'>(request));
      return {
        body: await file.blob.text(), contentType: 'text/plain; charset=utf-8', status: 201,
        headers: { 'Content-Disposition': `attachment; filename="${file.fileName}"` },
      };
    }, security('createAdminKeyBatch', request))),
    http.post(mswPath('revokeAdminKeyBatch'), ({ params, request }) => jsonResponse(
      () => repository.revokeAdminKeyBatch(oneParam(params.batchId)), undefined, security('revokeAdminKeyBatch', request),
    )),
    http.post(mswPath('resetAdminWriterTrials'), ({ params, request }) => jsonResponse(
      async () => repository.resetAdminWriterTrials(oneParam(params.userId), await body<'resetAdminWriterTrials'>(request)), undefined,
      security('resetAdminWriterTrials', request),
    )),
  ];
}
