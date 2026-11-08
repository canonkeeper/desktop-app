import { http } from 'msw';
import type { MockRepository } from '../../api/mock/repository';
import type { OperationId, OperationRequest } from '../../shared/api';
import { readFeedbackImageForm, readJson } from './handler-input';
import { binaryResponse, jsonResponse, mswPath, oneParam, type MswCsrfState } from './handler-runtime';

/** «Связаться с нами»: the contacts and the feedback of authors, guests and the administrator (specs/2026-11-07-contacts.md). */
export function createContactHandlers(repository: MockRepository, csrfState: MswCsrfState) {
  const security = (operationId: OperationId, request: Request) => ({ operationId, request, repository, csrfState });
  const body = async <TOperation extends OperationId>(request: Request) => (await readJson(request)) as unknown as OperationRequest<TOperation>;
  return [
    http.get(mswPath('getContacts'), ({ request }) => jsonResponse(() => repository.getContacts(), undefined, security('getContacts', request))),
    http.get(mswPath('getAdminContacts'), ({ request }) => jsonResponse(() => repository.getAdminContacts(), undefined, security('getAdminContacts', request))),
    http.put(mswPath('setAdminContacts'), ({ request }) => jsonResponse(
      async () => repository.setAdminContacts(await body<'setAdminContacts'>(request)), undefined, security('setAdminContacts', request),
    )),
    http.post(mswPath('uploadFeedbackImage'), ({ request }) => jsonResponse(
      async () => repository.uploadFeedbackImage(await readFeedbackImageForm(request)), { status: 201 }, security('uploadFeedbackImage', request),
    )),
    http.post(mswPath('createFeedbackThread'), ({ request }) => jsonResponse(
      async () => repository.createFeedbackThread(await body<'createFeedbackThread'>(request)), { status: 201 }, security('createFeedbackThread', request),
    )),
    http.get(mswPath('listFeedbackThreads'), ({ request }) => jsonResponse(() => repository.listFeedbackThreads(), undefined, security('listFeedbackThreads', request))),
    http.get(mswPath('getFeedbackThread'), ({ params, request }) => jsonResponse(
      () => repository.getFeedbackThread(oneParam(params.threadId)), undefined, security('getFeedbackThread', request),
    )),
    http.post(mswPath('addFeedbackMessage'), ({ params, request }) => jsonResponse(
      async () => repository.addFeedbackMessage(oneParam(params.threadId), await body<'addFeedbackMessage'>(request)), { status: 201 },
      security('addFeedbackMessage', request),
    )),
    http.get(mswPath('getFeedbackImage'), ({ params, request }) => binaryResponse(async () => {
      const file = await repository.getFeedbackImage(oneParam(params.threadId), oneParam(params.imageId));
      return { body: file.content, contentType: file.mimeType, cacheControl: 'private, max-age=3600' };
    }, security('getFeedbackImage', request))),
    http.get(mswPath('listAdminFeedback'), ({ request }) => jsonResponse(() => repository.listAdminFeedback(), undefined, security('listAdminFeedback', request))),
    http.get(mswPath('getAdminFeedbackThread'), ({ params, request }) => jsonResponse(
      () => repository.getAdminFeedbackThread(oneParam(params.threadId)), undefined, security('getAdminFeedbackThread', request),
    )),
    http.post(mswPath('replyAdminFeedback'), ({ params, request }) => jsonResponse(
      async () => repository.replyAdminFeedback(oneParam(params.threadId), await body<'replyAdminFeedback'>(request)), { status: 201 },
      security('replyAdminFeedback', request),
    )),
    http.put(mswPath('setAdminFeedbackStatus'), ({ params, request }) => jsonResponse(
      async () => repository.setAdminFeedbackStatus(oneParam(params.threadId), await body<'setAdminFeedbackStatus'>(request)), undefined,
      security('setAdminFeedbackStatus', request),
    )),
  ];
}
