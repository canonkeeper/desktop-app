import type { OperationId, OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { spendsImage } from './gallery-state';
import { mockDefault, moscowDay } from './quota';
import { mockEffectiveTier, mockPlanSettings } from './tiers';

type IllustrationAccess = Schema<'IllustrationAccess'>;
type AccessState = Schema<'IllustrationAccessState'>;

const day = 24 * 60 * 60 * 1000;

/** The edition of the documents the author's confirmation of the rights to his images names (R15.4). */
export const mockUploadEdition = '2026-11-04';

function nextMoscowMidnight(now: Date) {
  return new Date(Date.parse(`${moscowDay(now)}T00:00:00+03:00`) + day).toISOString();
}

/** What a right is made of (specs/2026-11-08-subscription-tiers.md TR6.1). */
type MockAccess = { blocked: boolean; imagesPerDay: number; trialImages: number; trialUsed: number };

export function trialLeftOf(access: MockAccess) {
  return Math.max(0, access.trialImages - access.trialUsed);
}

/** The right as the backend decides it: blocked, by the day while images_per_day is above zero, a trial, none. */
export function stateOf(access: MockAccess): AccessState {
  if (access.blocked) return 'blocked';
  if (access.imagesPerDay > 0) return 'active';
  return trialLeftOf(access) > 0 ? 'trial' : 'none';
}

/**
 * The right to draw in the mock (specs/2026-11-08-subscription-tiers.md TR6): drawing by the day on the tier in force,
 * else the trial drawings of the tier; the administrator only blocks it; a drawing of an account that may not draw is
 * 403 illustrations.locked.
 */
export function createIllustrationAccess(context: MockRepositoryContext) {
  const gallery = () => context.state.gallery;

  /** images_per_day of the author: his tier's, else the platform's, else the code's. */
  function tierImagesPerDay(userId: string) {
    return mockPlanSettings(context.state, userId).images_per_day ?? mockDefault(context.state.quotaDefaults, 'images_per_day');
  }

  function accessOf(userId: string, imagesPerDay = tierImagesPerDay(userId)): MockAccess {
    return {
      blocked: Boolean(gallery().access.blocked[userId]), imagesPerDay,
      trialImages: mockEffectiveTier(context.state, userId).limits.trialImages, trialUsed: context.state.trialImagesUsed[userId] ?? 0,
    };
  }

  /** Drawings and region edits the account started on the Moscow day and did not get back (R13.6). */
  function drawingsToday(userId: string, now = new Date()) {
    const today = moscowDay(now);
    const onToday = (at: string) => moscowDay(new Date(at)) === today;
    return gallery().images.reduce((total, image) => total
      + (image.createdBy === userId && onToday(image.createdAt) && spendsImage(image) ? 1 : 0)
      + image.layers.filter((layer) => layer.createdBy === userId && onToday(layer.createdAt) && spendsImage(layer)).length, 0);
  }

  /** The images the account uploaded on the Moscow day (image_uploads_per_day, R15.7). */
  function uploadsToday(userId: string, now = new Date()) {
    const today = moscowDay(now);
    return gallery().images.filter((image) => image.kind === 'upload' && image.createdBy === userId
      && moscowDay(new Date(image.createdAt)) === today).length;
  }

  /** The right as the author reads it; the writer card of the admin panel says nothing of uploads, as the backend's. */
  function viewOf(userId: string, imagesPerDay = tierImagesPerDay(userId), withUploads = true): IllustrationAccess {
    const access = accessOf(userId, imagesPerDay);
    const now = new Date();
    const uploads = gallery().uploads;
    return {
      state: stateOf(access), trialLeft: trialLeftOf(access),
      images: { used: drawingsToday(userId, now), limit: imagesPerDay }, resetsAt: nextMoscowMidnight(now),
      ...(uploads.enabled && withUploads ? {
        uploads: {
          consentVersion: mockUploadEdition, consented: uploads.consents[userId] === mockUploadEdition,
          today: { used: uploadsToday(userId, now), limit: mockDefault(context.state.quotaDefaults, 'image_uploads_per_day') },
        },
      } : {}),
    };
  }

  function locked(operationId: OperationId) {
    return new MockApiError(403, 'no right to draw', operationId, 'illustrations.locked');
  }

  /**
   * The right and quota step of the drawings the web starts (TR6.1): by the day counts images_per_day; a trial spends
   * trial drawings and a request it cannot cover whole is locked, counted for the day without its check. The first
   * style sketches of a saga without a style are free once per project. Returns how many of count may start and what
   * they spent.
   */
  function reserveDrawings(operationId: OperationId, count: number, free = false): { allowed: number; charge: 'quota' | 'trial' | null } {
    const userId = context.currentPrincipalId();
    const access = accessOf(userId);
    const state = stateOf(access);
    if (state !== 'trial' && state !== 'active') throw locked(operationId);
    if (free) return { allowed: count, charge: null };
    if (state === 'trial') {
      if (trialLeftOf(access) < count) throw locked(operationId);
      context.state.trialImagesUsed[userId] = access.trialUsed + count;
      return { allowed: count, charge: 'trial' };
    }
    const left = access.imagesPerDay - drawingsToday(userId);
    if (left < 1) throw new MockApiError(429, 'Daily drawing limit is used up.', operationId, 'quota.images_daily');
    return { allowed: Math.min(left, count), charge: 'quota' };
  }

  /** A drawing stopped by the provider's limit gives back its trial drawing, never below none used (TR6.2). */
  function returnTrial(userId: string) {
    context.state.trialImagesUsed[userId] = Math.max(0, (context.state.trialImagesUsed[userId] ?? 0) - 1);
  }

  /**
   * The style sketches (R8.6): the first set of a saga without a style is free once per project,
   * later sets count as drawings; a refused set takes nothing.
   */
  function reserveSketches(operationId: OperationId, projectId: string, count: number) {
    const free = !gallery().styles[projectId] && !gallery().styleImages[projectId] && !gallery().access.freeSketches[projectId];
    const { allowed } = reserveDrawings(operationId, count, free);
    if (free) gallery().access.freeSketches[projectId] = true;
    return { allowed, free };
  }

  /** A drawing button and the helper ask only for an author who may draw (TR6.3). */
  function requireDrawingRight(operationId: OperationId) {
    const state = stateOf(accessOf(context.currentPrincipalId()));
    if (state !== 'trial' && state !== 'active') throw locked(operationId);
  }

  async function getIllustrationAccess(): Promise<OperationResponse<'getIllustrationAccess', '200'>> {
    context.applyOperation('getIllustrationAccess');
    if (!context.state.session) throw new MockApiError(401, 'authentication required', 'getIllustrationAccess', 'auth.session_required');
    return clone(viewOf(context.currentPrincipalId()));
  }

  /** «Выключить рисунки» and «Включить обратно» of the admin panel (TR6.4). */
  function setBlock(operationId: OperationId, userId: string, body: OperationRequest<'setAdminWriterIllustrations'>) {
    if (body.state !== 'none' && body.state !== 'blocked') throw new MockApiError(400, 'validation failed', operationId, 'request.validation');
    if (body.state === 'blocked') gallery().access.blocked[userId] = true;
    else delete gallery().access.blocked[userId];
  }

  return { viewOf, reserveDrawings, reserveSketches, requireDrawingRight, returnTrial, setBlock, getIllustrationAccess, uploadsToday };
}

/** getIllustrationAccess of the mock API. */
export function createIllustrationAccessOperations(context: MockRepositoryContext) {
  const { getIllustrationAccess } = createIllustrationAccess(context);
  return { getIllustrationAccess };
}
