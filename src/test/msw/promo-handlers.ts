import { http } from 'msw';
import type { MockRepository } from '../../api/mock/repository';
import type { OperationId, OperationRequest } from '../../shared/api';
import { readJson } from './handler-input';
import {
  binaryResponse, idempotentJsonResponse, jsonResponse, mswPath, oneParam, type MswCsrfState, type MswIdempotencyRecord,
} from './handler-runtime';

/**
 * The operations of the public links: the permissions for the promotion of CanonKeeper
 * (specs/2026-10-30-promo-permissions.md) and the blind comparison of texts (specs/2026-11-05-reading-test.md).
 */
export function createPromoHandlers(repository: MockRepository, csrfState: MswCsrfState, idempotencyStore: Map<string, MswIdempotencyRecord>) {
  const security = (operationId: OperationId, request: Request) => ({ operationId, request, repository, csrfState });
  const body = <TOperation extends OperationId>(request: Request) => async () => (await readJson(request)) as unknown as OperationRequest<TOperation>;
  return [
    http.get(mswPath('getAdminPromoSagas'), ({ params, request }) =>
      jsonResponse(() => repository.getAdminPromoSagas(oneParam(params.userId)), undefined, security('getAdminPromoSagas', request))),
    http.post(mswPath('createAdminPromoPermission'), ({ params, request }) => idempotentJsonResponse(
      (options, input) => repository.createAdminPromoPermission(oneParam(params.userId), input as OperationRequest<'createAdminPromoPermission'>, options),
      { status: 201 }, security('createAdminPromoPermission', request), () => readJson(request), idempotencyStore,
    )),
    http.get(mswPath('listAdminPromoPermissions'), ({ request }) =>
      jsonResponse(() => repository.listAdminPromoPermissions(), undefined, security('listAdminPromoPermissions', request))),
    http.post(mswPath('takeAdminPromoFragment'), ({ params, request }) => jsonResponse(
      async () => repository.takeAdminPromoFragment(oneParam(params.permissionId), await body<'takeAdminPromoFragment'>(request)()),
      undefined, security('takeAdminPromoFragment', request),
    )),
    http.get(mswPath('getAdminPromoIllustration'), ({ params, request }) => binaryResponse(async () => {
      const file = await repository.getAdminPromoIllustration(oneParam(params.permissionId), Number(oneParam(params.number)));
      return {
        body: file.content, contentType: file.mimeType, cacheControl: file.cacheControl,
        headers: { 'content-disposition': `attachment; filename="${file.fileName}"` },
      };
    }, security('getAdminPromoIllustration', request))),
    http.get(mswPath('listPromoPermissions'), ({ request }) =>
      jsonResponse(() => repository.listPromoPermissions(), undefined, security('listPromoPermissions', request))),
    http.post(mswPath('answerPromoPermission'), ({ params, request }) => jsonResponse(
      async () => repository.answerPromoPermission(oneParam(params.permissionId), await body<'answerPromoPermission'>(request)()),
      undefined, security('answerPromoPermission', request),
    )),
    http.post(mswPath('revokePromoPermission'), ({ params, request }) =>
      jsonResponse(() => repository.revokePromoPermission(oneParam(params.permissionId)), undefined, security('revokePromoPermission', request))),
    http.post(mswPath('takeReadingTestPair'), ({ params, request }) => jsonResponse(
      async () => repository.takeReadingTestPair(oneParam(params.slug), await body<'takeReadingTestPair'>(request)()),
      undefined, security('takeReadingTestPair', request),
    )),
    http.post(mswPath('voteReadingTestPair'), ({ params, request }) => jsonResponse(
      async () => repository.voteReadingTestPair(oneParam(params.slug), await body<'voteReadingTestPair'>(request)()),
      undefined, security('voteReadingTestPair', request),
    )),
    http.post(mswPath('readPromoLink'), ({ request }) =>
      jsonResponse(async () => repository.readPromoLink(await body<'readPromoLink'>(request)()), undefined, security('readPromoLink', request))),
    http.post(mswPath('answerPromoLink'), ({ request }) =>
      jsonResponse(async () => repository.answerPromoLink(await body<'answerPromoLink'>(request)()), undefined, security('answerPromoLink', request))),
    http.post(mswPath('revokePromoLink'), ({ request }) =>
      jsonResponse(async () => repository.revokePromoLink(await body<'revokePromoLink'>(request)()), undefined, security('revokePromoLink', request))),
  ];
}
