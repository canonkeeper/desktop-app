import type { OperationId, OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import type { MockChapterCharge, MockState, MockTierPeriod } from './state';

type Tier = Schema<'SubscriptionTier'>;
type TierCode = Schema<'SubscriptionTierCode'>;
type Period = Schema<'SubscriptionPeriod'>;
type QuotaKey = Schema<'AdminQuotaKey'>;

const maxInteger = 2_147_483_647;
const day = 24 * 60 * 60 * 1000;

function running(period: Period, at: number) {
  return Date.parse(period.startsAt) <= at && (period.endsAt === null || at < Date.parse(period.endsAt));
}

function over(period: Period, at: number) {
  return period.endsAt !== null && (at >= Date.parse(period.endsAt) || period.endsAt === period.startsAt);
}

/** The tier in force (TR2.1): the highest running period of the account, else «Бесплатно». */
export function mockEffectiveTier(state: MockState, userId: string, at = Date.now()): Tier {
  const periods = state.tierPeriods.filter((period) => period.userId === userId && running(period, at));
  const rank = (code: TierCode) => state.tiers.find((tier) => tier.code === code)?.rank ?? -1;
  const code = periods.reduce<TierCode>((best, period) => rank(period.tier) > rank(best) ? period.tier : best, 'free');
  return state.tiers.find((tier) => tier.code === code)!;
}

/**
 * The plan of the quota fence (TR2.2, specs/2026-11-11-tier-limits.md TL2.1): every key of the core the tier in force sets,
 * the books of one saga among them; one saga holds all the text the account does.
 */
export function mockPlanSettings(state: MockState, userId: string): Partial<Record<QuotaKey, number>> {
  const { limits } = mockEffectiveTier(state, userId);
  return {
    chat_turns_per_day: limits.chatTurnsPerDay, active_turns: limits.activeTurns, imports_per_day: limits.importsPerDay,
    projects: limits.projects, project_books: limits.projectBooks, project_chapters: limits.accountChapters, project_characters: limits.accountCharacters,
    account_books: limits.books, account_chapters: limits.accountChapters, account_characters: limits.accountCharacters,
    images_per_day: limits.imagesPerDay, chapter_drafts_per_day: limits.chapterDraftsPerDay, drawing_turns_per_day: limits.drawingTurnsPerDay,
    draft_turns_per_day: limits.draftTurnsPerDay, draft_rewrites_per_day: limits.draftRewritesPerDay,
  };
}


/** The tier in force of a writer of the admin list and its end (specs/2026-11-08-admin-redesign.md AR3.1). */
export function mockWriterTier(state: MockState, userId: string, at = Date.now()): Schema<'AdminWriterTier'> {
  const tier = mockEffectiveTier(state, userId, at);
  return { code: tier.code, title: tier.title, until: untilOf(state, userId, at) };
}

function untilOf(state: MockState, userId: string, at: number): string | null {
  const tier = mockEffectiveTier(state, userId, at);
  if (tier.code === 'free') return null;
  const same = state.tierPeriods.filter((period) => period.userId === userId && period.tier === tier.code);
  let end = 0;
  for (const period of same.filter((candidate) => running(candidate, at))) {
    if (period.endsAt === null) return null;
    end = Math.max(end, Date.parse(period.endsAt));
  }
  for (let extended = true; extended;) {
    extended = false;
    for (const period of same.filter((candidate) => !over(candidate, at) && Date.parse(candidate.startsAt) <= end)) {
      if (period.endsAt === null) return null;
      if (Date.parse(period.endsAt) > end) {
        end = Date.parse(period.endsAt);
        extended = true;
      }
    }
  }
  return new Date(end).toISOString();
}

/** The new chapters that count (TR7.3): not given back, and their Job works or wrote a scene. */
function countedChapters(state: MockState, userId: string) {
  const counted = state.chapterCharges.filter((charge) => charge.userId === userId && !charge.returned && charge.job !== 'none');
  return {
    trials: counted.filter((charge) => charge.kind === 'trial').length,
    window: counted.filter((charge) => charge.kind === 'window').map((charge) => Date.parse(charge.chargedAt)),
    last: counted.reduce<number | null>((last, charge) => Math.max(last ?? 0, Date.parse(charge.chargedAt)), null),
  };
}

/**
 * What the window of a tier holds at the moment (TL3.1, domain/subscriptions Window.Count): the new chapters begun in its
 * last days, and when fewer than its number will be left — null while a place is free.
 */
function windowCount(limits: Tier['limits'], window: number[], at: number) {
  if (limits.chaptersPerWindow === null || limits.chapterEveryDays === null) return { used: 0, frees: null };
  const span = limits.chapterEveryDays * day;
  const inWindow = window.filter((moment) => moment > at - span).sort((a, b) => a - b);
  const used = inWindow.length;
  return { used, frees: used < limits.chaptersPerWindow ? null : inWindow[used - limits.chaptersPerWindow]! + span };
}

const iso = (at: number | null) => (at === null ? null : new Date(at).toISOString());

/** A new chapter of the draft counts already (TR7.3): its scenes are not charged again. */
export function mockDraftChapterCounts(state: MockState, draftId: string) {
  return state.chapterCharges.some((charge) => charge.draftId === draftId && !charge.returned && charge.job !== 'none');
}

/**
 * The first scenes of a draft the writing agent writes as a new chapter of the writer's tier (TR7.2, TR7.4, TL3.1): a
 * draft whose new chapter counts is not charged again; a window of N new chapters in K days refuses one while N began in
 * its last K days (429 tiers.chapter_wait), a tier without one once its trial chapters count (403 tiers.chapter_none).
 */
export function mockChargeNewChapter(state: MockState, userId: string, draftId: string, jobId: string, at = Date.now()) {
  if (mockDraftChapterCounts(state, draftId)) return;
  const { limits } = mockEffectiveTier(state, userId, at);
  const chapters = countedChapters(state, userId);
  let kind: MockChapterCharge['kind'] = 'window';
  if (limits.chapterEveryDays !== null) {
    if (windowCount(limits, chapters.window, at).frees !== null) {
      throw new MockApiError(429, 'The window of new chapters of the tier is full.', 'startChapterDraftJob', 'tiers.chapter_wait');
    }
  } else if (chapters.trials < limits.trialChapters) {
    kind = 'trial';
  } else {
    throw new MockApiError(403, 'The tier has no new chapter left.', 'startChapterDraftJob', 'tiers.chapter_none');
  }
  state.chapterCharges.push({ userId, draftId, jobId, kind, chargedAt: new Date(at).toISOString(), job: 'working', returned: false });
}

/** The charge of a Job of scenes counts for good with its first scene, and is given back by its end without one (TR7.3). */
export function mockSettleChapter(state: MockState, job: { id: string; mode?: string; status: string; done: number }) {
  const charge = state.chapterCharges.find((candidate) => candidate.jobId === job.id && candidate.job === 'working');
  if (!charge) return;
  if (job.done >= 1) charge.job = 'scene';
  else if (!['queued', 'running', 'canceling'].includes(job.status)) charge.job = 'none';
}

/**
 * What the tier in force lets the account talk (TR8.1): the minutes of a day — the mock runs no call, so they stay
 * whole — else a trial call while one is left, else nothing.
 */
export function mockVoice(state: MockState, userId: string) {
  const { limits } = mockEffectiveTier(state, userId);
  const trialCallsLeft = Math.max(0, limits.trialVoiceCalls - (state.trialVoiceCallsUsed[userId] ?? 0));
  const kind: 'daily' | 'trial' | 'none' = limits.voiceMinutesPerDay > 0 ? 'daily' : trialCallsLeft > 0 ? 'trial' : 'none';
  return { kind, minutesPerDay: limits.voiceMinutesPerDay, secondsLeft: kind === 'daily' ? limits.voiceMinutesPerDay * 60 : 0,
    trialCallsLeft, callSeconds: limits.trialVoiceCallMinutes * 60 };
}

/**
 * The plan of an account (TR9.8): the tier in force, its end, the periods that run or wait, then those ended in 90 days,
 * the trial drawings and calls left — the trial of the tier less those used (TR6.1, TR8.1) — the trial new chapters left
 * and when the next new chapter of the window begins (TR7.6), and the voice of today.
 */
export function mockAccountPlan(state: MockState, userId: string): Schema<'AccountPlan'> {
  const at = Date.now();
  const own = state.tierPeriods.filter((period) => period.userId === userId);
  const current = own.filter((period) => !over(period, at)).sort((a, b) => Date.parse(a.startsAt) - Date.parse(b.startsAt));
  const ended = own.filter((period) => over(period, at) && Date.parse(period.endsAt!) > at - 90 * day)
    .sort((a, b) => Date.parse(b.endsAt!) - Date.parse(a.endsAt!));
  const strip = ({ userId: _userId, ...period }: MockTierPeriod): Period => period;
  const tier = mockEffectiveTier(state, userId, at);
  const chapters = countedChapters(state, userId);
  const window = windowCount(tier.limits, chapters.window, at);
  return clone({
    tier, until: untilOf(state, userId, at), periods: [...current, ...ended].map(strip),
    trials: {
      imagesLeft: Math.max(0, tier.limits.trialImages - (state.trialImagesUsed[userId] ?? 0)),
      chaptersLeft: Math.max(0, tier.limits.trialChapters - chapters.trials),
      voiceCallsLeft: Math.max(0, tier.limits.trialVoiceCalls - (state.trialVoiceCallsUsed[userId] ?? 0)),
    },
    chapter: {
      perWindow: tier.limits.chaptersPerWindow, windowDays: tier.limits.chapterEveryDays, used: window.used, nextAt: iso(window.frees),
      lastAt: iso(chapters.last),
    },
    voice: (({ kind, minutesPerDay, secondsLeft, callSeconds }) => ({ kind, minutesPerDay, secondsLeftToday: secondsLeft, callSeconds }))(mockVoice(state, userId)),
  });
}

function invalid(operationId: OperationId) {
  return new MockApiError(400, 'validation failed', operationId, 'request.validation');
}

function validCount(value: unknown, min = 0) {
  return typeof value === 'number' && Number.isInteger(value) && value >= min && value <= maxInteger;
}

/** The tiers of the site as the backend keeps them (specs/2026-11-08-subscription-tiers.md TR1, TR3, TR9.8). */
export function createTierOperations(context: MockRepositoryContext) {
  function adminOperation(operationId: OperationId) {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
    if (context.state.session.user.impersonation) throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
  }

  async function listSubscriptionTiers(): Promise<OperationResponse<'listSubscriptionTiers', '200'>> {
    context.applyOperation('listSubscriptionTiers');
    return clone({ tiers: context.state.tiers });
  }

  async function getAccountPlan(): Promise<OperationResponse<'getAccountPlan', '200'>> {
    context.applyOperation('getAccountPlan');
    return mockAccountPlan(context.state, context.currentPrincipalId());
  }

  async function listAdminSubscriptionTiers(): Promise<OperationResponse<'listAdminSubscriptionTiers', '200'>> {
    adminOperation('listAdminSubscriptionTiers');
    return clone({ tiers: context.state.tiers });
  }

  async function setAdminSubscriptionTier(
    code: TierCode,
    body: OperationRequest<'setAdminSubscriptionTier'>,
  ): Promise<OperationResponse<'setAdminSubscriptionTier', '200'>> {
    adminOperation('setAdminSubscriptionTier');
    const tier = context.state.tiers.find((candidate) => candidate.code === code);
    if (!tier) throw new MockApiError(404, 'Resource not found', 'setAdminSubscriptionTier', 'not_found');
    const { limits, lotUrl } = body;
    const counts = [limits.projects, limits.projectBooks, limits.books, limits.chatTurnsPerDay, limits.imagesPerDay, limits.trialImages,
      limits.trialChapters, limits.chapterDraftsPerDay, limits.voiceMinutesPerDay, limits.trialVoiceCalls, limits.accountChapters,
      limits.drawingTurnsPerDay, limits.draftTurnsPerDay, limits.draftRewritesPerDay];
    const window = [limits.chaptersPerWindow, limits.chapterEveryDays];
    if (counts.some((value) => !validCount(value)) || !Number.isSafeInteger(limits.accountCharacters) || limits.accountCharacters < 0
      || !validCount(limits.trialVoiceCallMinutes, 1) || !validCount(limits.importsPerDay, 1) || !validCount(limits.activeTurns, 1)
      || (window.some((value) => value === null) ? window.some((value) => value !== null) : window.some((value) => !validCount(value, 1)))
      || (lotUrl !== null && (!lotUrl.startsWith('https://funpay.com/') || lotUrl.length > 300))
      || (code === 'free' && lotUrl !== null)) {
      throw invalid('setAdminSubscriptionTier');
    }
    Object.assign(tier, { lotUrl, limits: { ...limits } });
    return clone(tier);
  }

  async function grantAdminWriterTier(
    userId: string,
    body: OperationRequest<'grantAdminWriterTier'>,
  ): Promise<OperationResponse<'grantAdminWriterTier', '201'>> {
    adminOperation('grantAdminWriterTier');
    const terms = [body.days !== undefined, body.until !== undefined, body.forever !== undefined].filter(Boolean).length;
    const startsAt = new Date();
    let endsAt: string | null = null;
    if (terms !== 1 || (body.days !== undefined && (!Number.isInteger(body.days) || body.days < 1 || body.days > 3660))) {
      throw invalid('grantAdminWriterTier');
    }
    if (body.days !== undefined) endsAt = new Date(startsAt.getTime() + body.days * day).toISOString();
    if (body.until !== undefined) {
      const today = new Date(startsAt.getTime() + 3 * 60 * 60 * 1000).toISOString().slice(0, 10);
      const end = Date.parse(`${body.until}T00:00:00+03:00`) + day;
      if (!/^\d{4}-\d{2}-\d{2}$/.test(body.until) || Number.isNaN(end) || body.until <= today) throw invalid('grantAdminWriterTier');
      endsAt = new Date(end).toISOString();
    }
    const period: MockTierPeriod = {
      userId, id: globalThis.crypto.randomUUID(), tier: body.tier, startsAt: startsAt.toISOString(), endsAt, source: 'admin',
    };
    context.state.tierPeriods.push(period);
    const { userId: _userId, ...created } = period;
    return clone(created);
  }

  async function endAdminWriterPeriod(userId: string, periodId: string): Promise<OperationResponse<'endAdminWriterPeriod', '200'>> {
    adminOperation('endAdminWriterPeriod');
    const period = context.state.tierPeriods.find((candidate) => candidate.id === periodId && candidate.userId === userId);
    if (!period) throw new MockApiError(404, 'Resource not found', 'endAdminWriterPeriod', 'not_found');
    const at = Date.now();
    if (over(period, at)) throw new MockApiError(409, 'The period is over.', 'endAdminWriterPeriod', 'tiers.period_over');
    period.endsAt = Date.parse(period.startsAt) > at ? period.startsAt : new Date(at).toISOString();
    const { userId: _userId, ...ended } = period;
    return clone(ended);
  }

  /**
   * «Вернуть пробные» (TR3.4): the trials the body names are whole again — the trial drawings and calls the writer used
   * are 0, his trial new chapters are given back; the answer is his plan.
   */
  async function resetAdminWriterTrials(
    userId: string,
    body: OperationRequest<'resetAdminWriterTrials'>,
  ): Promise<OperationResponse<'resetAdminWriterTrials', '200'>> {
    adminOperation('resetAdminWriterTrials');
    const kinds = Object.entries(body ?? {});
    if (kinds.length === 0 || kinds.some(([kind, value]) => value !== true || !['images', 'chapters', 'voiceCalls'].includes(kind))) {
      throw invalid('resetAdminWriterTrials');
    }
    if (body.images) delete context.state.trialImagesUsed[userId];
    if (body.chapters) {
      for (const charge of context.state.chapterCharges) if (charge.userId === userId && charge.kind === 'trial') charge.returned = true;
    }
    if (body.voiceCalls) delete context.state.trialVoiceCallsUsed[userId];
    return mockAccountPlan(context.state, userId);
  }

  return {
    listSubscriptionTiers, getAccountPlan, listAdminSubscriptionTiers, setAdminSubscriptionTier, grantAdminWriterTier, endAdminWriterPeriod,
    resetAdminWriterTrials,
  };
}
