// SPDX-License-Identifier: AGPL-3.0-only
import type { CanonKeeperApiClient, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { desktopLinked } from './desktop-account';
import { openSagaOfFile } from './saga-file';
import { noSiteSagaTransfer } from './state';

type SagaTransferOperations = Pick<
  CanonKeeperApiClient,
  'getSagaTransferRequest' | 'startSagaTransfer' | 'getSiteSagaTransfer' | 'startSiteSagaTransfer' | 'cancelSiteSagaTransfer'
>;

/** How long a request of a transfer and its code wait (specs/2026-11-16-desk-site-saga.md TR3.1, TR3.3). */
const transferLifetimeMs = 10 * 60_000;

/**
 * The saga the mock transfers: its name, the name of its file as the site gives it, its 12 MiB and what the site left out —
 * a chat it could not read, chapters at work and drawings of the workshops, what of «Недавно удалённые» and the questions of
 * the other members —, so the words of the end of a transfer are seen.
 */
const transferredSaga = {
  name: 'Хроники Белого порта', file: 'Хроники Белого порта.cksaga', bytes: 12 * 1024 * 1024,
  chatsNotRead: ['Кто убил капитана'], sections: ['workshops/writing/drafts.jsonl', 'workshops/drawings/drawings.jsonl'],
  leftOut: { drafts: 3, drawings: 5, trashedBooks: 1, trashedChapters: 2, othersTurns: 4 },
};

/**
 * The transfer of a saga of the site to the app (specs/2026-11-16-desk-site-saga.md): the page /transfer of the site reads the
 * request the app opened and gives the code of the saga the author owns, with the address of the return the server builds from
 * the port; the window of the app starts, reads and stops its one transfer. The mock has neither an app on the side of the
 * site nor a browser on the side of the app: the page takes any request as one of the account unless a scenario says another
 * account or a request gone, and a transfer of the window waits until its term or its stop unless a scenario moves it on.
 */
export function createSagaTransferOperations(context: MockRepositoryContext): SagaTransferOperations {
  let serial = 0;
  /** The request of the app waits for this author, or the site refuses it as the server does (`sagaTransferRequest`). */
  function requestWaits(requestId: string, operationId: 'getSagaTransferRequest' | 'startSagaTransfer') {
    const refusal = context.getScenario().sagaTransferRequest;
    if (refusal === 'other_account') throw new MockApiError(409, 'the app is linked with another account', operationId, 'saga_transfer.other_account');
    if (!requestId || refusal === 'expired') throw new MockApiError(410, 'the request of the transfer does not wait any more', operationId, 'saga_transfer.expired');
  }

  function signedIn(operationId: 'getSiteSagaTransfer' | 'startSiteSagaTransfer' | 'cancelSiteSagaTransfer') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
  }

  /** The transfer as the backend holds it now: a waiting that ran out failed for its term. */
  function current(): Schema<'SiteSagaTransfer'> {
    const transfer = context.state.siteSagaTransfer;
    if (transfer.phase === 'waiting' && transfer.expiresAt && Date.parse(transfer.expiresAt) <= Date.now()) {
      context.state.siteSagaTransfer = { ...noSiteSagaTransfer(), id: transfer.id, phase: 'failed', code: 'expired' };
    }
    return structuredClone(context.state.siteSagaTransfer);
  }

  /**
   * The next phase of a transfer that goes, by the scenario (`siteSagaTransfer`): the return of the browser spends the waiting —
   * the site prepares the saga (no byte yet) —, the saga comes, it opens as a saga file opens, and it is open; a failure ends it
   * with its code, the file gone. The mock moves one phase a read, as the backend that goes on between two reads of the page.
   */
  function goOn() {
    const outcome = context.getScenario().siteSagaTransfer;
    const transfer = context.state.siteSagaTransfer;
    if (!outcome || !transfer.id || !['waiting', 'downloading', 'opening'].includes(transfer.phase)) return;
    if (outcome !== 'done') {
      context.state.siteSagaTransfer = { ...noSiteSagaTransfer(), id: transfer.id, phase: 'failed', code: outcome };
      return;
    }
    const comes = { ...noSiteSagaTransfer(), id: transfer.id, name: transferredSaga.file };
    if (transfer.phase === 'waiting') context.state.siteSagaTransfer = { ...comes, phase: 'downloading', bytes: 0 };
    else if (transfer.phase === 'downloading' && !transfer.bytes) context.state.siteSagaTransfer = { ...comes, phase: 'downloading', bytes: transferredSaga.bytes };
    else if (transfer.phase === 'downloading') context.state.siteSagaTransfer = { ...comes, phase: 'opening', bytes: transferredSaga.bytes };
    else {
      const opened = openSagaOfFile(context, {
        from: 'site', name: transferredSaga.name, createdAt: new Date().toISOString(), chatsNotRead: [...transferredSaga.chatsNotRead],
        sections: [...transferredSaga.sections], leftOut: { ...transferredSaga.leftOut },
      });
      context.state.siteSagaTransfer = { ...noSiteSagaTransfer(), id: transfer.id, phase: 'done', projectId: opened.projectId, opened };
    }
  }

  return {
    async getSagaTransferRequest(requestId) {
      context.applyOperation('getSagaTransferRequest');
      // As the server: a session opened on the author's behalf reads no request (TR3.2, as /link).
      if (context.state.session?.user.impersonation) {
        throw new MockApiError(403, 'only the author himself transfers a saga', 'getSagaTransferRequest', 'account.own_session_required');
      }
      requestWaits(requestId, 'getSagaTransferRequest');
      return { expiresAt: new Date(Date.now() + transferLifetimeMs).toISOString() };
    },

    async startSagaTransfer(body) {
      context.applyOperation('startSagaTransfer');
      const request = (body ?? {}) as Record<string, unknown>;
      const valid = typeof request.request === 'string' && request.request.length > 0 && typeof request.projectId === 'string' &&
        typeof request.state === 'string' && /^[A-Za-z0-9._~-]{1,128}$/u.test(request.state) &&
        Number.isInteger(request.port) && Number(request.port) >= 20000 && Number(request.port) <= 29999 && Object.keys(request).length === 4;
      if (!valid) throw new MockApiError(400, 'validation failed', 'startSagaTransfer', 'request.validation');
      // As the server: only the author himself in his own session gives a code, and the request is read before the saga.
      if (context.state.session?.user.impersonation) {
        throw new MockApiError(403, 'only the author himself transfers a saga', 'startSagaTransfer', 'account.own_session_required');
      }
      // As the server: an address not confirmed gives no code.
      if (context.state.session && !context.state.session.user.emailVerified) {
        throw new MockApiError(403, 'email address is not verified', 'startSagaTransfer', 'auth.email_unverified');
      }
      requestWaits(String(request.request), 'startSagaTransfer');
      const saga = context.state.projects.find((project) => project.id === request.projectId);
      if (!saga) throw new MockApiError(404, 'saga not found', 'startSagaTransfer', 'not_found');
      if (saga.currentMembership.role !== 'owner') throw new MockApiError(403, 'only the owner transfers a saga', 'startSagaTransfer', 'forbidden');
      const query = new URLSearchParams({ code: 'mock_transfer_code_000000000000000000000000', state: String(request.state) });
      return {
        callbackUrl: `http://127.0.0.1:${Number(request.port)}/desktop/transfer/callback?${query.toString()}`,
        expiresAt: new Date(Date.now() + transferLifetimeMs).toISOString(),
      };
    },

    async getSiteSagaTransfer() {
      signedIn('getSiteSagaTransfer');
      current();
      goOn();
      return current();
    },

    async startSiteSagaTransfer() {
      signedIn('startSiteSagaTransfer');
      const transfer = current();
      if (transfer.phase === 'downloading' || transfer.phase === 'opening') {
        throw new MockApiError(409, 'a transfer downloads or opens', 'startSiteSagaTransfer', 'saga_transfer.running');
      }
      if (!desktopLinked(context)) throw new MockApiError(409, 'the app is not linked with an account', 'startSiteSagaTransfer', 'account.not_linked');
      // A new transfer is a new one, as the backend's are: its number never repeats one put away.
      serial += 1;
      const query = new URLSearchParams({ port: '24567', request: `00000000-0000-4000-8000-${String(serial).padStart(12, '0')}`, state: 'mock-transfer-state' });
      context.state.siteSagaTransfer = {
        ...noSiteSagaTransfer(), id: `00000000-0000-4000-9000-${String(serial).padStart(12, '0')}`, phase: 'waiting',
        address: `https://canonkeeper.example.test/transfer?${query.toString()}`, expiresAt: new Date(Date.now() + transferLifetimeMs).toISOString(),
        email: context.state.desktopAccount.account?.email ?? null,
      };
      return current();
    },

    async cancelSiteSagaTransfer(body) {
      signedIn('cancelSiteSagaTransfer');
      const request = (body ?? {}) as Record<string, unknown>;
      if (typeof request.id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u.test(request.id) || Object.keys(request).length !== 1) {
        throw new MockApiError(400, 'invalid request body', 'cancelSiteSagaTransfer', 'validation.failed');
      }
      const transfer = current();
      // As the server: another transfer — put away or replaced by a new start meanwhile — is answered as it is.
      if (!transfer.id || transfer.id !== request.id) return transfer;
      if (transfer.phase === 'opening') throw new MockApiError(409, 'the saga already opens', 'cancelSiteSagaTransfer', 'saga_transfer.opening');
      if (transfer.phase === 'waiting' || transfer.phase === 'downloading') {
        context.state.siteSagaTransfer = { ...noSiteSagaTransfer(), id: transfer.id, phase: 'failed', code: 'canceled' };
      } else {
        context.state.siteSagaTransfer = noSiteSagaTransfer();
      }
      return current();
    },
  };
}
