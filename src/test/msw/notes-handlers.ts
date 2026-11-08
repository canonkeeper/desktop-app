import { http } from 'msw';
import type { MockRepository } from '../../api/mock/repository';
import { MockApiError } from '../../api/mock/scenarios';
import type { OperationId, OperationRequest } from '../../shared/api';
import { readJson } from './handler-input';
import { emptyResponse, idempotentJsonResponse, jsonResponse, mswPath, oneParam, type MswCsrfState, type MswIdempotencyRecord } from './handler-runtime';

export function createNotesHandlers(repository: MockRepository, csrfState: MswCsrfState, idempotencyStore: Map<string, MswIdempotencyRecord>) {
  const security = (operationId: OperationId, request: Request) => ({ operationId, request, repository, csrfState });
  return [
    http.post(mswPath('listNotes'), ({ request }) => jsonResponse(async () => {
      if (new URL(request.url).search) throw new MockApiError(400, 'Notes query must be sent in the request body.', 'listNotes', 'validation_failed');
      const input: unknown = await request.json().catch(() => { throw new MockApiError(400, 'Invalid notes JSON.', 'listNotes', 'validation_failed'); });
      return repository.listNotes(input as OperationRequest<'listNotes'>);
    }, undefined, security('listNotes', request))),
    http.post(mswPath('createPersonalNote'), ({ request }) => idempotentJsonResponse((options, body) => repository.createPersonalNote(body as OperationRequest<'createPersonalNote'>, options), { status: 201 }, security('createPersonalNote', request), () => readJson(request), idempotencyStore)),
    http.get(mswPath('getPersonalNote'), ({ params, request }) => jsonResponse(() => repository.getPersonalNote(oneParam(params.noteId)), undefined, security('getPersonalNote', request))),
    http.patch(mswPath('updatePersonalNote'), ({ params, request }) => jsonResponse(async () => repository.updatePersonalNote(oneParam(params.noteId), await readJson(request) as OperationRequest<'updatePersonalNote'>), undefined, security('updatePersonalNote', request))),
    http.delete(mswPath('deletePersonalNote'), ({ params, request }) => emptyResponse(() => repository.deletePersonalNote(oneParam(params.noteId), Number(new URL(request.url).searchParams.get('expectedRevision'))), security('deletePersonalNote', request))),
    http.get(mswPath('resolvePersonalNoteSource'), ({ params, request }) => jsonResponse(() => repository.resolvePersonalNoteSource(oneParam(params.noteId)), undefined, security('resolvePersonalNoteSource', request))),
  ];
}
