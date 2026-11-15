// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import type { MockReadingRest, MockState } from './state';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { accountUnlinked, desktopLinked, mockSiteHost } from './desktop-account';

type ReadingOperation =
  | 'getReadingEstimate' | 'getReadingConsent' | 'setReadingConsent' | 'getReadingSample' | 'startReadingSample' | 'cancelReadingSample';
type Sample = Schema<'ReadingSample'>;

/** The three chapters a sample of the mock reads, as the board 44·27 shows them. */
const sampleCards: Sample['chapters'] = [
  { chapterId: '00000000-0000-4000-8000-000000000101', place: 'Книга 1, гл. 1 „Туманный берег“', summary: 'Ирна возвращается в северную гавань и узнаёт, что маяк погас.', events: ['Возвращение Ирны', 'маяк погас'], people: ['Ирна', 'смотритель Тарвин'] },
  { chapterId: '00000000-0000-4000-8000-000000000102', place: 'Книга 1, гл. 2 „Приказ“', summary: 'Тарвин получает приказ закрыть гавань до весны и спорит с советом.', events: ['Приказ из столицы', 'спор в ратуше'], people: ['Тарвин', 'капитан Сольвейг'] },
  { chapterId: '00000000-0000-4000-8000-000000000103', place: 'Книга 1, гл. 3 „Погасший маяк“', summary: 'Ирна поднимается на погасший маяк и находит чужие следы.', events: ['Ночь на маяке', 'чужие следы'], people: ['Ирна', 'капитан Сольвейг'] },
];
type Consent = Schema<'ReadingConsent'>;
type Reading = Schema<'IndexingReading'>;
type Preparation = Schema<'IndexingPreparation'>;

/** The host the reading of the settings goes to: the site of the account, the provider of «Свой ИИ», or none. */
function hostOf(state: MockState): string | null {
  if (state.aiSettings.source === 'account') return mockSiteHost;
  const own = state.aiSettings.own;
  return own ? new URL(own.baseUrl).host : null;
}

/**
 * The limits of the tier of the account for the reading the site told (specs/2026-11-13-desk-account-ai.md AI3.7); null —
 * the site has not told them yet.
 */
function limitsOf(context: MockRepositoryContext): NonNullable<Reading['limits']> | null {
  if (context.scenario.readingLimits === null) return null;
  return context.scenario.readingLimits ?? {
    characters: { used: 3_200_000, limit: 40_000_000 }, work: { used: 19_000_000, limit: 320_000_000 },
  };
}

/** Who reads the books by the settings now (the table «Страницам B2»): «Свой ИИ», the account, or nobody. */
function sourceOf(state: MockState): Reading['source'] {
  const source = state.aiSettings.source;
  return source === 'own' || source === 'account' ? source : null;
}

/** The unit of a sum in the settings now: the currency of both prices, or tokens — the account counts in tokens. */
function unitOf(state: MockState): Consent['unit'] {
  if (state.aiSettings.source === 'account') return 'tokens';
  const own = state.aiSettings.own;
  if (!own || own.advanced.priceInPerMillion === null || own.advanced.priceOutPerMillion === null) return 'tokens';
  return own.advanced.currency ?? 'USD';
}

/**
 * The sagas read under a consent that acts with a stop sum in money of the currency now (specs/2026-11-13-desk-consent-web.md
 * RW3.1): what getAiSettings says, by the rule of the domain — the consent acts for the host of the settings, its sum is in
 * money and in the currency the prices are in; the live sagas only.
 */
export function moneyStopSagasOf(state: MockState): number {
  const host = hostOf(state);
  const unit = unitOf(state);
  return Object.entries(state.readingConsents).filter(([projectId, consent]) => state.projects.some((project) => project.id === projectId)
    && consent.state !== 'none' && consent.host === host && consent.stopAt !== null && consent.unit !== 'tokens' && consent.unit === unit).length;
}

/** What a chapter read by the mock costs: a chapter of its sample (CN3.3). */
const chapterPrice = { inputTokens: 16_000, outputTokens: 6_500, amount: 1_300_000 };

/**
 * How the cards Job of a saga reads its chapters in the mock (specs/2026-11-13-desk-consent.md CN3.1–CN3.4, GW4.3;
 * specs/2026-11-13-desk-consent-web.md, the trace; specs/2026-11-13-desk-account-ai.md AI3.4, AI3.5): on the site and without
 * a helper — every chapter at once, as before; in the app with «Свой ИИ» or the helper of the account — none without a consent
 * of its host that is allowed, at a pause or with the account unlinked, and under one a chapter a step of the Job, counted by
 * the consent, until the next chapter would pass the stop sum: then the reading pauses at «сумма» (the account has no sum).
 */
export type CardsReading = { kind: 'all' } | { kind: 'none' } | { kind: 'paid'; pay(): boolean };

export function cardsReadingOf(context: MockRepositoryContext, projectId: string): CardsReading {
  const source = context.state.aiSettings.source;
  if (source !== 'own' && source !== 'account') return { kind: 'all' };
  const consent = context.state.readingConsents[projectId];
  if (!consent || consent.state !== 'allowed' || consent.host !== hostOf(context.state) || context.scenario.readingPause
    || accountUnlinked(context)) return { kind: 'none' };
  return {
    kind: 'paid',
    pay() {
      const price = consent.unit === 'tokens' ? chapterPrice.inputTokens + chapterPrice.outputTokens : chapterPrice.amount;
      if (consent.stopAt !== null && consent.spent.amount + price > consent.stopAt) {
        Object.assign(consent, { state: 'paused', pause: 'sum' } satisfies Partial<Consent>);
        return false;
      }
      consent.spent = {
        ...consent.spent, amount: consent.spent.amount + price,
        inputTokens: consent.spent.inputTokens + chapterPrice.inputTokens, outputTokens: consent.spent.outputTokens + chapterPrice.outputTokens,
      };
      return true;
    },
  };
}

/**
 * The consent of a saga as it stands (CN3.1, CN3.4): none for another host — with provider_changed —, paused at the reason
 * of the scenario when one is set.
 */
function standing(context: MockRepositoryContext, projectId: string): Consent | null {
  const kept = context.state.readingConsents[projectId];
  if (!kept || kept.state === 'none') return null;
  const pause = context.scenario.readingPause;
  return pause ? { ...structuredClone(kept), state: 'paused', pause } : structuredClone(kept);
}

/** The seed of the marks a scenario sows, or none when it sows nothing. */
function seedOf(context: MockRepositoryContext): string | null {
  const { readingUnread, readingUnreadReason, readingNamesWait, readingNamesUnread, readingThreadsUnread, readingRestAgain } = context.scenario;
  const seed = { readingUnread, readingUnreadReason, readingNamesWait, readingNamesUnread, readingThreadsUnread, readingRestAgain };
  return Object.values(seed).some((value) => value !== undefined) ? JSON.stringify(seed) : null;
}

/**
 * The marks of the reading of a saga (specs/2026-11-15-desk-reading-rest.md RR2.5): the state of the saga, sown by the scenario —
 * a scenario that sows other marks sows them anew, one that sows none leaves them as they are.
 */
export function readingRestOf(context: MockRepositoryContext, projectId: string): MockReadingRest {
  const seed = seedOf(context);
  const kept = context.state.readingRest[projectId];
  if (kept && (seed === null || seed === kept.seed)) return kept;
  const { scenario } = context;
  const names = scenario.readingNamesUnread ?? (scenario.readingNamesWait ? 'wait' : null);
  const threads = scenario.readingThreadsUnread ?? (scenario.readingNamesWait ? 'wait' : null);
  const sown: MockReadingRest = {
    chapters: scenario.readingUnread ?? 0, chaptersReason: scenario.readingUnreadReason ?? 'cut_answer', names, threads,
    again: scenario.readingRestAgain ? { names: scenario.readingNamesUnread ?? null, threads: scenario.readingThreadsUnread ?? null } : null,
    seed: seed ?? '',
  };
  context.state.readingRest[projectId] = sown;
  return sown;
}

/** «Повторить» forgets the marks of the saga: the chapters read again, the names and the threads wait to be brought together. */
function forgetRest(rest: MockReadingRest) {
  rest.chapters = 0;
  if (rest.names !== null) rest.names = 'wait';
  if (rest.threads !== null) rest.threads = 'wait';
}

/**
 * A step of the worker of the mock once every chapter is read (RR2.5): what waits is brought together, or marked again with its
 * reasons when the scenario sowed that.
 */
export function bringRestTogether(state: MockState, projectId: string) {
  const rest = state.readingRest[projectId];
  if (!rest) return;
  if (rest.names === 'wait') rest.names = rest.again?.names ?? null;
  if (rest.threads === 'wait') rest.threads = rest.again?.threads ?? null;
}

/** Whether the names or the threads of a saga wait for a step of the worker. */
export function restWaits(state: MockState, projectId: string) {
  const rest = state.readingRest[projectId];
  return rest?.names === 'wait' || rest?.threads === 'wait';
}

/**
 * The reading of the books in the status of a saga of the desktop app (CN3.8) by the counts of the mock: what it waits for —
 * the consent or a pause — and its state; nothing on the site and nothing while the helper is not connected. The marks act only
 * under a consent that acts (specs/2026-11-15-desk-reading-rest.md RR1.4, RR2.2): without one all that is marked waits.
 */
export function readingStatusOf(
  context: MockRepositoryContext, projectId: string, counts: Preparation,
): { waiting?: 'consent' | 'paused'; reading?: Reading } {
  const source = context.state.aiSettings.source;
  const desktop = source === 'own' || source === 'account' || context.scenario.consentMissing || context.state.readingConsents[projectId];
  if (!desktop || context.scenario.helperMissing || accountUnlinked(context)) return {};
  const kept = context.state.readingConsents[projectId];
  // The reading stops as the server stops it: the consent that acts stands at the reason until the author lets it go on.
  const stops = context.scenario.readingStops;
  if (stops && kept?.state === 'allowed' && kept.host === hostOf(context.state)) Object.assign(kept, { state: 'paused', pause: stops } satisfies Partial<Consent>);
  const consent = standing(context, projectId);
  const acting = consent !== null && consent.host === hostOf(context.state);
  const rest = readingRestOf(context, projectId);
  // The names and the threads stand after the chapters: a saga sown with them has every chapter read but those that did not.
  const read = rest.names !== null || rest.threads !== null ? Math.max(counts.chaptersTotal - rest.chapters, 0) : counts.chaptersRead;
  const unread = acting ? rest.chapters : 0;
  const left = Math.max(counts.chaptersTotal - read - unread, 0);
  const waits = (value: MockReadingRest['names']) => value === 'wait' || (!acting && value !== null);
  const marked = (value: MockReadingRest['names']) => (acting && value !== null && value !== 'wait' ? value : null);
  const namesWait = left === 0 && waits(rest.names);
  const threadsWait = left === 0 && waits(rest.threads);
  const unreadRest = left === 0 && (marked(rest.names) || marked(rest.threads)) ? { names: marked(rest.names), threads: marked(rest.threads) } : null;
  const pending = left > 0 || namesWait || threadsWait;
  const reading: Reading = {
    state: 'reading', source: sourceOf(context.state), reason: null, pause: null, chaptersRead: read, chaptersTotal: counts.chaptersTotal,
    namesWait, threadsWait, unread: unread > 0 ? { chapters: unread, reason: rest.chaptersReason } : null, unreadRest,
    spent: 0, approximate: false, stopAt: null, step: null, unit: unitOf(context.state), remainingSeconds: null,
  };
  if (!acting || !consent) {
    if (!pending) return {};
    return {
      waiting: 'consent',
      reading: { ...reading, state: 'waiting_consent', reason: kept && kept.state !== 'none' ? 'provider_changed' : null },
    };
  }
  Object.assign(reading, {
    spent: consent.spent.amount, approximate: consent.spent.approximate, stopAt: consent.stopAt, step: consent.step, unit: consent.unit,
  });
  const limits = source === 'account' ? limitsOf(context) : null;
  if (limits) reading.limits = limits;
  if (consent.state === 'paused') return { waiting: 'paused', reading: { ...reading, state: 'paused', pause: consent.pause } };
  if (pending) return { reading: { ...reading, remainingSeconds: left > 0 ? Math.max(left * 30, 60) : null } };
  return unread > 0 || unreadRest ? { reading: { ...reading, state: 'done' } } : {};
}

/**
 * The reading of the books of a saga by «Свой ИИ» of the desktop app (specs/2026-11-13-desk-consent.md): the estimate the
 * window of the consent shows and the consent of each saga, by the rules of backend/internal/domain/readingconsent the
 * pages meet.
 */
export function createReadingOperations(context: MockRepositoryContext) {
  /** The host the reading goes to while the helper is connected: «Свой ИИ» or the site of the account. */
  function connected(operationId: ReadingOperation): string {
    const { source, own } = context.state.aiSettings;
    if (source === 'account' && context.state.aiAccount && desktopLinked(context)) return mockSiteHost;
    if (source !== 'own' || !own || !own.chatModel || !own.cardsModel) {
      throw new MockApiError(409, 'the helper is not connected', operationId, 'helper.not_connected');
    }
    return new URL(own.baseUrl).host;
  }

  function account(): boolean {
    return context.state.aiSettings.source === 'account';
  }

  function owned(projectId: string, operationId: ReadingOperation) {
    context.applyOperation(operationId);
    context.projectFor(projectId, operationId, ['owner']);
  }

  function unit(): Consent['unit'] {
    return unitOf(context.state);
  }

  function none(): Consent {
    return {
      state: 'none', source: sourceOf(context.state), reason: null, host: null, pause: null, unit: unit(), stopAt: null, step: null, since: null,
      spent: { inputTokens: 0, cachedInputTokens: 0, outputTokens: 0, amount: 0, approximate: false },
    };
  }

  function invalid(path: string): MockApiError {
    return new MockApiError(400, 'a field is not valid', 'setReadingConsent', 'reading.invalid', {
      fields: { errors: [{ path, message: 'not valid', code: 'reading.invalid' }] },
    });
  }

  function noSample(): Sample {
    return {
      state: 'none', failure: null, done: 0, total: 0, chapters: [], seconds: 0,
      spent: { inputTokens: 0, outputTokens: 0, amount: unit() === 'tokens' ? null : 0, unit: unit(), approximate: false },
    };
  }

  return {
    async getReadingEstimate(projectId: string): Promise<OperationResponse<'getReadingEstimate', '200'>> {
      owned(projectId, 'getReadingEstimate');
      const host = connected('getReadingEstimate');
      if (account()) {
        // The helper of the account (AI3.4): characters and the two limits of the tier instead of money, no sample, no sum;
        // the limits the site has not told yet are not known. What is left is of the scenario: the chapters cost characters
        // and work, the names and the threads only work.
        const limits = limitsOf(context);
        const left = context.scenario.readingEstimateLeft ?? { chapters: 612 };
        const rest = Boolean(left.namesWait || left.threadsWait);
        const characters = left.chapters * 10_000;
        const work = Math.round(left.chapters * 26_700_000 / 612) + (rest ? 1_200_000 : 0);
        const short = (limit: Schema<'ReadingLimit'>, needed: number) => Math.max(limit.limit - limit.used, 0) / needed;
        const byCharacters = limits && characters > 0 ? short(limits.characters, characters) : 1;
        const byWork = limits && work > 0 ? short(limits.work, work) : 1;
        const endsFirst = byCharacters >= 1 && byWork >= 1 ? null : byCharacters <= byWork ? 'characters' : 'work';
        return {
          chapters: left.chapters, inputTokens: 21_900_000, outputTokens: 4_800_000, seconds: Math.round(left.chapters * 39_600 / 612) + (rest ? 600 : 0),
          amount: null, unit: 'tokens', layerShare: 0.28,
          host, local: false, kind: 'rough', assumptions: ['calibration_fallback', 'reasoning', 'names_by_cards', 'layer_by_structure', 'repeats_margin'],
          suggestedStop: 0, sampleChapters: 0, chapterEdit: { inputTokens: 176_400, outputTokens: 23_000, amount: null },
          namesWait: left.namesWait ?? false, threadsWait: left.threadsWait ?? false,
          account: { characters, work, charactersLimit: limits?.characters ?? null, workLimit: limits?.work ?? null, endsFirst },
        };
      }
      const own = context.state.aiSettings.own!;
      const priced = unit() !== 'tokens';
      return {
        chapters: 612, inputTokens: 21_900_000, outputTokens: 4_800_000, seconds: 19_800,
        amount: priced ? 680_000_000 : null, unit: unit(), layerShare: 0.28,
        host, local: own.preset === 'ollama' || host.startsWith('127.0.0.1') || host.startsWith('localhost'), kind: 'rough',
        assumptions: ['calibration_fallback', 'no_reasoning', 'names_by_cards', 'layer_by_structure', 'repeats_margin'],
        suggestedStop: priced ? 1_000_000_000 : 40_000_000, sampleChapters: 3,
        chapterEdit: { inputTokens: 176_400, outputTokens: 23_000, amount: priced ? 6_000_000 : null },
        namesWait: false, threadsWait: false,
      };
    },

    async getReadingConsent(projectId: string): Promise<OperationResponse<'getReadingConsent', '200'>> {
      owned(projectId, 'getReadingConsent');
      const consent = standing(context, projectId);
      if (consent && consent.host === hostOf(context.state)) return { ...consent, source: sourceOf(context.state) };
      return { ...none(), reason: consent ? 'provider_changed' : null };
    },

    async setReadingConsent(projectId: string, body: OperationRequest<'setReadingConsent'>): Promise<OperationResponse<'setReadingConsent', '200'>> {
      owned(projectId, 'setReadingConsent');
      const host = connected('setReadingConsent');
      if (account() && (body.stopAt !== undefined && body.stopAt !== null || body.continueBy !== undefined)) {
        // The consent of the helper of the account has no stop sum (AI3.4).
        throw invalid(body.continueBy !== undefined ? '/continueBy' : '/stopAt');
      }
      const kept = context.state.readingConsents[projectId];
      const acting = kept && kept.state !== 'none' && kept.host === host ? structuredClone(kept) : null;
      if (body.state === 'paused' && !acting) {
        throw new MockApiError(409, 'no consent to pause', 'setReadingConsent', 'reading.no_consent');
      }
      const next: Consent = acting ?? { ...none(), state: 'allowed', host, since: new Date().toISOString() };
      if (next.unit !== unit() && (body.stopAt !== undefined || body.continueBy !== undefined || next.stopAt === null)) {
        next.unit = unit();
        if (next.unit !== 'tokens') next.spent.amount = 0;
      }
      if (body.stopAt !== undefined && body.continueBy !== undefined) throw invalid('/state');
      if (body.stopAt === null) {
        next.stopAt = null;
        next.step = null;
      } else if (body.stopAt !== undefined) {
        if (body.stopAt <= next.spent.amount) throw invalid('/stopAt');
        next.stopAt = body.stopAt;
        next.step = body.stopAt;
      } else if (body.continueBy !== undefined) {
        next.stopAt = next.spent.amount + body.continueBy;
        next.step = body.continueBy;
      }
      if (body.state === 'allowed' && next.stopAt !== null && next.stopAt <= next.spent.amount) throw invalid('/stopAt');
      next.state = body.state;
      next.source = sourceOf(context.state);
      next.pause = body.state === 'paused' ? 'author' : null;
      context.state.readingConsents[projectId] = next;
      // «Повторить» and a new consent forget the marks of the saga with the write (specs/2026-11-15-desk-reading-rest.md RR1.7).
      if (body.state === 'allowed' && (body.retryUnread === true || !acting)) forgetRest(readingRestOf(context, projectId));
      return structuredClone(next);
    },

    /** The sample reads a chapter each time the page asks how it goes, and is done at the third (CN2.4). */
    async getReadingSample(projectId: string): Promise<OperationResponse<'getReadingSample', '200'>> {
      owned(projectId, 'getReadingSample');
      const sample = context.state.readingSamples[projectId];
      if (!sample) return noSample();
      const failure = context.scenario.sampleFailure;
      if (failure && (sample.state === 'queued' || sample.state === 'running')) {
        Object.assign(sample, { state: 'failed', failure } satisfies Partial<Sample>);
      }
      if (sample.state === 'queued' || sample.state === 'running') {
        const done = Math.min(sample.done + 1, sample.total);
        Object.assign(sample, {
          state: done === sample.total ? 'succeeded' : 'running', done, chapters: sampleCards.slice(0, done), seconds: done * 80,
          spent: { ...sample.spent, inputTokens: done * 16_000, outputTokens: done * 6_500, amount: sample.spent.amount === null ? null : done * 1_300_000, approximate: true },
        } satisfies Partial<Sample>);
      }
      return structuredClone(sample);
    },

    async startReadingSample(projectId: string): Promise<OperationResponse<'startReadingSample', '202'>> {
      owned(projectId, 'startReadingSample');
      const host = connected('startReadingSample');
      if (account()) throw new MockApiError(409, 'the helper of the account has no sample', 'startReadingSample', 'reading.no_sample');
      const consent = context.state.readingConsents[projectId];
      if (consent && consent.state !== 'none' && consent.host === host) {
        throw new MockApiError(409, 'a consent acts', 'startReadingSample', 'reading.consent_given');
      }
      const live = context.state.readingSamples[projectId];
      if (!live || (live.state !== 'queued' && live.state !== 'running')) {
        context.state.readingSamples[projectId] = { ...noSample(), state: 'queued', total: sampleCards.length };
      }
      return structuredClone(context.state.readingSamples[projectId]!);
    },

    async cancelReadingSample(projectId: string): Promise<void> {
      owned(projectId, 'cancelReadingSample');
      const sample = context.state.readingSamples[projectId];
      if (sample && (sample.state === 'queued' || sample.state === 'running')) sample.state = 'canceled';
    },
  };
}
