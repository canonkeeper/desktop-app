import type { DownloadedFile, OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import type { MockKeyBatch, MockTierPeriod } from './state';
import { mockAccountPlan } from './tiers';

type KeyCheck = Schema<'SubscriptionKeyCheck'>;
type Period = Schema<'SubscriptionPeriod'>;

const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
const day = 24 * 60 * 60 * 1000;
const failuresPerDay = 10;

/** A key of 25 characters of Crockford's alphabet, as the backend draws it (TR4.1). */
function newKey() {
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(25));
  return Array.from(bytes, (value) => alphabet[value & 31]).join('');
}

function display(key: string) {
  return `CK-${key.match(/.{1,5}/gu)!.join('-')}`;
}

/** The key as the author typed it (TR4.1): case, spaces, hyphens and CK do not matter, O is 0, I and L are 1. */
export function normalizeMockKey(input: string) {
  const text = input.toUpperCase().replace(/[\s\-–—_]/gu, '').replace(/O/gu, '0').replace(/[IL]/gu, '1');
  const key = text.length === 27 && text.startsWith('CK') ? text.slice(2) : text;
  return key.length === 25 && [...key].every((char) => alphabet.includes(char)) ? key : null;
}

/** The Moscow day of the moment. */
function moscow(at: number) {
  return { date: new Date(at + 3 * 60 * 60 * 1000).toISOString().slice(0, 10) };
}

function running(period: Period, at: number) {
  return Date.parse(period.startsAt) <= at && (period.endsAt === null || at < Date.parse(period.endsAt));
}

function over(period: Period, at: number) {
  return period.endsAt !== null && (at >= Date.parse(period.endsAt) || period.endsAt === period.startsAt);
}

/** What a key does to the periods of an account (TR5.1). */
function effectOf(tier: KeyCheck['tier'], days: number, periods: Period[], at: number): KeyCheck {
  let starts = at;
  let unneeded = false;
  const others: KeyCheck['running'] = [];
  for (const period of periods) {
    if (period.tier !== tier) {
      if (running(period, at)) others.push({ tier: period.tier, endsAt: period.endsAt });
    } else if (!over(period, at)) {
      if (period.endsAt === null) unneeded = true;
      else starts = Math.max(starts, Date.parse(period.endsAt));
    }
  }
  if (unneeded) return { tier, days, effect: 'unneeded', startsAt: null, endsAt: null, running: others };
  return {
    tier, days, effect: starts > at ? 'extends' : 'starts_now', running: others,
    startsAt: new Date(starts).toISOString(), endsAt: new Date(starts + days * day).toISOString(),
  };
}

function batchView(batch: MockKeyBatch): Schema<'AdminKeyBatch'> {
  const { keyRows, ...view } = batch;
  return clone({
    ...view,
    redeemed: keyRows.filter((row) => row.redeemedBy !== null).length,
    revoked: keyRows.filter((row) => row.revoked).length,
  });
}

/** The keys of the tiers as the backend keeps them (specs/2026-11-08-subscription-tiers.md TR4, TR5). */
export function createTierKeyOperations(context: MockRepositoryContext) {
  function adminOperation(operationId: 'listAdminKeyBatches' | 'createAdminKeyBatch' | 'revokeAdminKeyBatch') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
    if (context.state.session.user.impersonation) throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
  }

  /**
   * One attempt of the author with a key (TR5.4): his own verified session, the refusals of the Moscow day, then the key.
   * A refusal counts; the eleventh attempt of a day is not looked up.
   */
  function attempt(operationId: 'checkSubscriptionKey' | 'redeemSubscriptionKey', input: unknown) {
    context.applyOperation(operationId);
    const session = context.state.session;
    if (!session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
    if (session.user.impersonation) throw new MockApiError(403, 'Only the author himself enters his key.', operationId, 'tiers.own_session_required');
    if (!session.user.emailVerified) throw new MockApiError(403, 'email address is not verified', operationId, 'auth.email_unverified');
    if (typeof input !== 'string' || input.length > 200) throw new MockApiError(400, 'validation failed', operationId, 'request.validation');
    const userId = context.currentPrincipalId();
    const now = Date.now();
    const today = moscow(now);
    const failures = context.state.keyFailures[userId];
    const count = failures?.date === today.date ? failures.count : 0;
    if (count >= failuresPerDay) throw new MockApiError(429, 'no more attempts to enter a key today', operationId, 'tiers.key_attempts');
    const refuse = (status: number, code: string) => {
      context.state.keyFailures[userId] = { date: today.date, count: count + 1 };
      return new MockApiError(status, code, operationId, code);
    };
    const key = normalizeMockKey(input);
    const batch = key ? context.state.keyBatches.find((candidate) => candidate.keyRows.some((row) => row.key === key)) : undefined;
    const row = batch?.keyRows.find((candidate) => candidate.key === key);
    if (!batch || !row || row.revoked) throw refuse(422, 'tiers.key_invalid');
    if (row.redeemedBy !== null && row.redeemedBy !== userId) throw refuse(409, 'tiers.key_used');
    const periods = context.state.tierPeriods.filter((period) => period.userId === userId);
    return { batch, row, userId, now, periods };
  }

  async function checkSubscriptionKey(body: OperationRequest<'checkSubscriptionKey'>): Promise<OperationResponse<'checkSubscriptionKey', '200'>> {
    const { batch, row, now, periods } = attempt('checkSubscriptionKey', body.key);
    // A key the author himself entered is not the key of another, and no attempt (specs/2026-11-10-ux1-tariff-key.md UXK3.1).
    if (row.redeemedBy !== null) throw new MockApiError(409, 'tiers.key_own', 'checkSubscriptionKey', 'tiers.key_own');
    return clone(effectOf(batch.tier, batch.days, periods, now));
  }

  async function redeemSubscriptionKey(body: OperationRequest<'redeemSubscriptionKey'>): Promise<OperationResponse<'redeemSubscriptionKey', '200'>> {
    const { batch, row, userId, now, periods } = attempt('redeemSubscriptionKey', body.key);
    let period = periods.find((candidate) => candidate.id === row.periodId);
    if (!period) {
      const check = effectOf(batch.tier, batch.days, periods, now);
      if (check.effect === 'unneeded') throw new MockApiError(409, 'tiers.key_unneeded', 'redeemSubscriptionKey', 'tiers.key_unneeded');
      period = { userId, id: globalThis.crypto.randomUUID(), tier: batch.tier, startsAt: check.startsAt!, endsAt: check.endsAt, source: 'key' } satisfies MockTierPeriod;
      context.state.tierPeriods.push(period);
      Object.assign(row, { redeemedBy: userId, periodId: period.id });
    }
    const { userId: _owner, ...shown } = period;
    return clone({ plan: mockAccountPlan(context.state, userId), period: shown });
  }

  async function listAdminKeyBatches(): Promise<OperationResponse<'listAdminKeyBatches', '200'>> {
    adminOperation('listAdminKeyBatches');
    return { batches: [...context.state.keyBatches].reverse().map(batchView) };
  }

  async function createAdminKeyBatch(body: OperationRequest<'createAdminKeyBatch'>): Promise<DownloadedFile> {
    adminOperation('createAdminKeyBatch');
    const note = body.note ?? null;
    if (!['artist', 'writer', 'pro'].includes(body.tier) || !Number.isInteger(body.count) || body.count < 1 || body.count > 500
      || (note !== null && (note.length === 0 || note.length > 120 || /[\r\n]/u.test(note) || note.trim() !== note))) {
      throw new MockApiError(400, 'validation failed', 'createAdminKeyBatch', 'request.validation');
    }
    const keys = Array.from({ length: body.count }, newKey);
    const createdAt = new Date().toISOString();
    context.state.keyBatches.push({
      id: globalThis.crypto.randomUUID(), tier: body.tier, days: 30, note, createdAt, createdBy: context.currentPrincipalId(),
      keys: body.count, redeemed: 0, revoked: 0, revokedAt: null,
      keyRows: keys.map((key) => ({ key, redeemedBy: null, periodId: null, revoked: false })),
    });
    const date = moscow(Date.parse(createdAt)).date;
    return {
      blob: new Blob([keys.map(display).join('\n')], { type: 'text/plain;charset=utf-8' }),
      fileName: `canonkeeper-${body.tier}-${date}-${body.count}.txt`,
    };
  }

  async function revokeAdminKeyBatch(batchId: string): Promise<OperationResponse<'revokeAdminKeyBatch', '200'>> {
    adminOperation('revokeAdminKeyBatch');
    const batch = context.state.keyBatches.find((candidate) => candidate.id === batchId);
    if (!batch) throw new MockApiError(404, 'Resource not found', 'revokeAdminKeyBatch', 'not_found');
    for (const row of batch.keyRows) if (row.redeemedBy === null) row.revoked = true;
    batch.revokedAt ??= new Date().toISOString();
    return batchView(batch);
  }

  return { checkSubscriptionKey, redeemSubscriptionKey, listAdminKeyBatches, createAdminKeyBatch, revokeAdminKeyBatch };
}
