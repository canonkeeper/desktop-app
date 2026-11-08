import type { OperationId, OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { spendsImage, type MockAccessRequest, type MockIllustrationRight } from './gallery-state';
import { mockDefault, moscowDay } from './quota';

type IllustrationAccess = Schema<'IllustrationAccess'>;
type AccessState = Schema<'IllustrationAccessState'>;
type AccessRequest = Schema<'IllustrationAccessRequest'>;



const day = 24 * 60 * 60 * 1000;

/** The edition of the documents the author's confirmation of the rights to his images names (R15.4). */
export const mockUploadEdition = '2026-11-04';

function nextMoscowMidnight(now: Date) {
  return new Date(Date.parse(`${moscowDay(now)}T00:00:00+03:00`) + day).toISOString();
}

export function stateOf(right: MockIllustrationRight, now = new Date()): AccessState {
  if (right.granted === 'blocked') return 'blocked';
  if (right.granted === 'active' && (right.activeUntil === null || moscowDay(now) <= right.activeUntil)) return 'active';
  return right.trialLeft > 0 ? 'trial' : 'none';
}

/**
 * The right to illustrations in the mock (specs/2026-10-29-saga-illustrations.md UC11): every
 * account starts with the trial of a new account (five unless the administrator set another), a drawing of an account that may not draw is 403
 * illustrations.locked, the author asks for access and the administrator turns it on or declines.
 */
export function createIllustrationAccess(context: MockRepositoryContext) {
  const access = () => context.state.gallery.access;

  /** The trial of a new account (R11.1): the platform's trial drawings, else the code's (admin-panel R7.3). */
  function newAccountTrial() {
    return mockDefault(context.state.quotaDefaults, 'trial_drawings');
  }

  function rightOf(userId: string): MockIllustrationRight {
    const rights = access().rights;
    rights[userId] ??= { granted: 'none', trialLeft: newAccountTrial(), activeUntil: null };
    return rights[userId]!;
  }

  /** Drawings and region edits the account started on the Moscow day and did not get back (R13.6). */
  function drawingsToday(userId: string, now = new Date()) {
    const today = moscowDay(now);
    const onToday = (at: string) => moscowDay(new Date(at)) === today;
    return context.state.gallery.images.reduce((total, image) => total
      + (image.createdBy === userId && onToday(image.createdAt) && spendsImage(image) ? 1 : 0)
      + image.layers.filter((layer) => layer.createdBy === userId && onToday(layer.createdAt) && spendsImage(layer)).length, 0);
  }

  /** The images the account uploaded on the Moscow day (image_uploads_per_day, R15.7). */
  function uploadsToday(userId: string, now = new Date()) {
    const today = moscowDay(now);
    return context.state.gallery.images.filter((image) => image.kind === 'upload' && image.createdBy === userId
      && moscowDay(new Date(image.createdAt)) === today).length;
  }

  function pendingOf(userId: string) {
    return access().requests.find((request) => request.userId === userId && request.status === 'pending');
  }

  function publicRequest({ userId: _userId, ...request }: MockAccessRequest): AccessRequest {
    return clone(request);
  }

  /** The right as the author reads it; the writer card of the admin panel says nothing of uploads, as the backend's. */
  function viewOf(userId: string, imagesPerDay: number = mockDefault(context.state.quotaDefaults, 'images_per_day'), withUploads = true): IllustrationAccess {
    const right = rightOf(userId);
    const pending = pendingOf(userId);
    const now = new Date();
    const state = stateOf(right, now);
    const uploads = context.state.gallery.uploads;
    return {
      state, trialLeft: right.trialLeft, activeUntil: state === 'active' ? right.activeUntil : null,
      images: { used: drawingsToday(userId, now), limit: imagesPerDay }, resetsAt: nextMoscowMidnight(now),
      request: pending ? publicRequest(pending) : null,
      ...(uploads.enabled && withUploads ? {
        uploads: {
          consentVersion: mockUploadEdition, consented: uploads.consents[userId] === mockUploadEdition,
          today: { used: uploadsToday(userId, now), limit: mockDefault(context.state.quotaDefaults, 'image_uploads_per_day') },
        },
      } : {}),
    };
  }

  /**
   * The access and quota step of the drawings the web starts (R11.2): the actor needs a trial or
   * active right; active access counts images_per_day, a trial also spends trial drawings and a
   * request it cannot cover whole is locked. The first style sketches of a saga without a style
   * are free once per project. Returns how many of count may start.
   */
  function reserveDrawings(operationId: OperationId, count: number, free = false) {
    const userId = context.currentPrincipalId();
    const right = rightOf(userId);
    const state = stateOf(right);
    if (state !== 'trial' && state !== 'active') {
      throw new MockApiError(403, 'no access to illustrations', operationId, 'illustrations.locked');
    }
    if (free) return count;
    if (state === 'trial' && right.trialLeft < count) {
      throw new MockApiError(403, 'no access to illustrations', operationId, 'illustrations.locked');
    }
    const left = mockDefault(context.state.quotaDefaults, 'images_per_day') - drawingsToday(userId);
    if (left < 1) throw new MockApiError(429, 'Daily drawing limit is used up.', operationId, 'quota.images_daily');
    const allowed = Math.min(left, count);
    if (state === 'trial') right.trialLeft -= allowed;
    return allowed;
  }

  /**
   * The style sketches (R8.6): the first set of a saga without a style is free once per project,
   * later sets count as drawings; a refused set takes nothing.
   */
  function reserveSketches(operationId: OperationId, projectId: string, count: number) {
    const gallery = context.state.gallery;
    const free = !gallery.styles[projectId] && !gallery.styleImages[projectId] && !gallery.access.freeSketches[projectId];
    const allowed = reserveDrawings(operationId, count, free);
    if (free) gallery.access.freeSketches[projectId] = true;
    return allowed;
  }

  /** A drawing button asks only for an author who may draw. */
  function requireDrawingRight(operationId: OperationId) {
    const state = stateOf(rightOf(context.currentPrincipalId()));
    if (state !== 'trial' && state !== 'active') {
      throw new MockApiError(403, 'no access to illustrations', operationId, 'illustrations.locked');
    }
  }

  async function getIllustrationAccess(): Promise<OperationResponse<'getIllustrationAccess', '200'>> {
    context.applyOperation('getIllustrationAccess');
    if (!context.state.session) throw new MockApiError(401, 'authentication required', 'getIllustrationAccess', 'auth.session_required');
    return clone(viewOf(context.currentPrincipalId()));
  }

  async function createIllustrationAccessRequest(
    body: OperationRequest<'createIllustrationAccessRequest'>,
  ): Promise<OperationResponse<'createIllustrationAccessRequest', '201'>> {
    const operationId = 'createIllustrationAccessRequest';
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'authentication required', operationId, 'auth.session_required');
    const message = body?.message?.trim() || null;
    if (message !== null && [...message].length > 1000) throw new MockApiError(400, 'validation failed', operationId, 'request.validation');
    const userId = context.currentPrincipalId();
    if (stateOf(rightOf(userId)) === 'active') throw new MockApiError(409, 'access to illustrations is already on', operationId, 'illustrations.active');
    const now = new Date();
    const pending = pendingOf(userId);
    if (pending) {
      if (now.getTime() < Date.parse(pending.sentAt) + day) {
        throw new MockApiError(429, 'the request was sent less than a day ago', operationId, 'illustrations.request_pending');
      }
      pending.sentAt = now.toISOString();
      if (message) pending.message = message;
      return publicRequest(pending);
    }
    const request: MockAccessRequest = {
      id: `illustration-request-${access().nextRequest++}`, userId, status: 'pending', message,
      createdAt: now.toISOString(), sentAt: now.toISOString(), decidedAt: null,
    };
    access().requests.push(request);
    return publicRequest(request);
  }

  /** «Включить», «Выключить», «Вернуть пробные» of the admin panel (R11.6); the pending request is granted with access. */
  function setRight(operationId: OperationId, userId: string, body: OperationRequest<'setAdminWriterIllustrations'>) {
    const invalid = () => new MockApiError(400, 'validation failed', operationId, 'request.validation');
    const today = moscowDay(new Date());
    if (!['none', 'active', 'blocked'].includes(body.state)) throw invalid();
    const until = body.until ?? null;
    if (until !== null && (body.state !== 'active' || !/^\d{4}-\d{2}-\d{2}$/u.test(until) || until < today)) throw invalid();
    const right = rightOf(userId);
    right.granted = body.state;
    right.activeUntil = body.state === 'active' ? until : null;
    if (body.resetTrial) right.trialLeft = newAccountTrial();
    const pending = pendingOf(userId);
    if (pending && stateOf(right) === 'active') {
      pending.status = 'granted';
      pending.decidedAt = new Date().toISOString();
    }
  }

  function requests() {
    return [...access().requests].sort((left, right) => (
      Number(right.status === 'pending') - Number(left.status === 'pending') || right.createdAt.localeCompare(left.createdAt)
    ));
  }

  function declineRequest(operationId: OperationId, requestId: string): MockAccessRequest {
    const request = access().requests.find((item) => item.id === requestId);
    if (!request) throw new MockApiError(404, 'not found', operationId, 'not_found');
    if (request.status !== 'pending') throw new MockApiError(409, 'Conflict', operationId, 'conflict');
    request.status = 'declined';
    request.decidedAt = new Date().toISOString();
    return request;
  }

  return {
    viewOf, reserveDrawings, reserveSketches, requireDrawingRight, setRight, requests, declineRequest, publicRequest, stateOf,
    rightOf, getIllustrationAccess, createIllustrationAccessRequest, uploadsToday,
  };
}

/** getIllustrationAccess and createIllustrationAccessRequest of the mock API. */
export function createIllustrationAccessOperations(context: MockRepositoryContext) {
  const { getIllustrationAccess, createIllustrationAccessRequest } = createIllustrationAccess(context);
  return { getIllustrationAccess, createIllustrationAccessRequest };
}
