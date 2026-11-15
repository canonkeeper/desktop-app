// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';
import { agentWorkflowScenario as fixture } from '../scenarios';
import type { MockScenarioControl } from '../scenarios';
import { createIdempotencyKey } from '../../../shared/api';

const saga = 'project-white-port';

/** The mock app linked with an account, its helper chosen, in the scenario of the test. */
async function theAccount(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.setScenario({ preset: 'normal', actorRole: 'owner', accountLinked: true });
  await api.updateAiSettings({ source: 'account' });
  api.setScenario({ preset: 'normal', actorRole: 'owner', accountLinked: true, ...scenario });
  return api;
}

// specs/2026-11-13-desk-account-ai.md checklist step 7: the mock answers the reading of the helper of the account — its consent
// at the site without a sum, its estimate in characters and the two limits, its pauses with the limits in the status, no
// sample —, the refusals of the start of a question by its counter, and whether the question of a failed turn came back.
describe('the mock of the helper of the account', () => {
  it('answers the estimate in characters and limits, the consent without a sum and no sample', async () => {
    const api = await theAccount({ readingLimits: { characters: { used: 39_000_000, limit: 40_000_000 }, work: { used: 0, limit: 320_000_000 } } });
    const estimate = await api.getReadingEstimate(saga);
    expect(estimate).toMatchObject({ amount: null, unit: 'tokens', sampleChapters: 0, suggestedStop: 0, host: 'canonkeeper.example.test' });
    expect(estimate.account).toMatchObject({ characters: expect.any(Number), endsFirst: 'characters', charactersLimit: { used: 39_000_000 } });
    await expect(api.setReadingConsent(saga, { state: 'allowed', stopAt: 1_000 })).rejects.toMatchObject({ code: 'reading.invalid' });
    await expect(api.startReadingSample(saga)).rejects.toMatchObject({ status: 409, code: 'reading.no_sample' });
    const consent = await api.setReadingConsent(saga, { state: 'allowed' });
    expect(consent).toMatchObject({ state: 'allowed', host: 'canonkeeper.example.test', unit: 'tokens', stopAt: null });
  });

  it.each(['characters', 'reading_fence', 'unfit', 'unavailable', 'connection'] as const)('answers the pause %s with the limits', async (pause) => {
    const api = await theAccount();
    await api.setReadingConsent(saga, { state: 'allowed' });
    api.setScenario({ preset: 'normal', actorRole: 'owner', accountLinked: true, readingPause: pause });
    const status = await api.getIndexingStatus(saga);
    expect(status.preparation?.waiting).toBe('paused');
    expect(status.reading).toMatchObject({ state: 'paused', pause, unit: 'tokens', limits: { characters: { limit: 40_000_000 }, work: { limit: 320_000_000 } } });
  });

  it('says the estimate does not know the limits the site has not told yet', async () => {
    const api = await theAccount({ readingLimits: null });
    const estimate = await api.getReadingEstimate(saga);
    expect(estimate.account).toMatchObject({ charactersLimit: null, workLimit: null, endsFirst: null });
  });

  it('names who reads the books in the state of the reading and in the consent', async () => {
    const api = await theAccount();
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'waiting_consent', source: 'account' });
    expect(await api.getReadingConsent(saga)).toMatchObject({ state: 'none', source: 'account' });
    await api.setReadingConsent(saga, { state: 'allowed' });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ source: 'account' });
    expect(await api.getReadingConsent(saga)).toMatchObject({ state: 'allowed', source: 'account' });
  });

  it('reads the books of the account only under its consent, not at its pause, and goes on once it is given', async () => {
    const api = await theAccount();
    const advance = async () => {
      for (let round = 0; round < 6; round += 1) {
        const jobs = (await api.listProjectJobs(saga, { limit: 100 })).data.filter((job) => job.status === 'queued' || job.status === 'running');
        for (const job of jobs) await api.getJob(job.id);
        await api.getIndexingStatus(saga);
      }
      return (await api.getIndexingStatus(saga)).reading;
    };
    const waiting = (await advance())!;
    expect(waiting).toMatchObject({ state: 'waiting_consent' });
    expect(waiting.chaptersRead).toBeLessThan(waiting.chaptersTotal);
    await api.setReadingConsent(saga, { state: 'allowed' });
    api.setScenario({ ...api.getScenario(), readingPause: 'characters' });
    const paused = await advance();
    expect(paused).toMatchObject({ state: 'paused', pause: 'characters', chaptersRead: waiting.chaptersRead });
    api.setScenario({ ...api.getScenario(), readingPause: undefined });
    // Under the consent the chapters are read: the state of the reading is gone once every chapter is read.
    const reading = await advance();
    expect(reading === undefined || reading.chaptersRead > waiting.chaptersRead).toBe(true);
  });

  it('after the unlinking waits for the helper and refuses the question; the line of the site names both purposes while linked', async () => {
    const api = await theAccount();
    expect((await api.getDesktopState()).network).toEqual([
      { host: 'canonkeeper.example.test', purpose: 'связь с аккаунтом: имя, тариф и остатки; вопросы помощнику и чтение книг' },
    ]);
    await api.setReadingConsent(saga, { state: 'allowed' });
    await api.unlinkAccount();
    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    const status = await api.getIndexingStatus(saga);
    expect(status.reading ?? null).toBeNull();
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: createIdempotencyKey('createChatSession') });
    await expect(api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') }))
      .rejects.toMatchObject({ status: 409, code: 'helper.not_connected' });
    expect((await api.getDesktopState()).network).toEqual([]);
  });

  it('names the site in the network while a link only waits, as the backend does', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    expect((await api.getDesktopState()).network).toEqual([]);
    await api.startAccountLink();
    expect((await api.getDesktopState()).network).toEqual([{ host: 'canonkeeper.example.test', purpose: 'связь с аккаунтом: имя, тариф и остатки' }]);
    await api.cancelAccountLink();
    expect((await api.getDesktopState()).network).toEqual([]);
  });

  it.each([
    ['ai.account_unreachable', 503, true],
    ['ai.account_unreachable', 503, false],
    ['ai.daily_fence', 429, undefined],
    ['ai.app_unfit', 409, undefined],
    ['app.request_too_large', 409, undefined],
    ['quota.chat_turns_daily', 429, undefined],
  ] as const)('refuses the start of a question by %s', async (code, status, mayReturn) => {
    const api = await theAccount({ accountTurnRefusal: { code, mayReturn } });
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const refused = api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    await expect(refused).rejects.toMatchObject({ status, code });
    if (mayReturn !== undefined) await expect(refused).rejects.toMatchObject({ problem: { mayReturn } });
  });

  it.each(['yes', 'no', 'pending', 'none'] as const)('says the question of a failed turn came back: %s', async (returned) => {
    const api = await theAccount({ turnFailure: 'model.site_unreachable', questionReturned: returned });
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const job = await api.getJob(turn.job.id);
    expect(job).toMatchObject({ status: 'failed', questionReturned: returned, error: { code: 'model.site_unreachable' } });
  });

  // specs/2026-11-15-desk-account-words-web.md, checklist step 2: every code of a failed turn of the account with each answer of
  // the count; a correction refused by the counter as a question is; the reading that stops as the server stops it; what is
  // left of a saga for the estimate.
  it.each([
    'model.daily_fence', 'model.account_unlinked', 'model.permit_ended', 'model.site_unavailable', 'model.app_unfit', 'model.thread_too_large',
    'model.site_unreachable', 'model.session_ended', 'quota.chat_turns_daily', 'quota.active_turns', 'model.server_overloaded', 'model.usage_limit_exceeded',
  ])('fails a turn of the account with %s', async (code) => {
    const api = await theAccount({ turnFailure: code, questionReturned: 'yes' });
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    expect(await api.getJob(turn.job.id)).toMatchObject({ status: 'failed', questionReturned: 'yes', error: { code } });
  });

  it.each([
    ['ai.account_unreachable', 503, true],
    ['ai.daily_fence', 429, undefined],
    ['ai.app_unfit', 409, undefined],
    ['app.request_too_large', 409, undefined],
    ['quota.chat_turns_daily', 429, undefined],
  ] as const)('refuses a correction by %s', async (code, status, mayReturn) => {
    const api = await theAccount();
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    api.setScenario({ ...api.getScenario(), accountTurnRefusal: { code, mayReturn } });
    const refused = api.steerChatTurn(chat.id, turn.job.subject.type === 'research_chat_turn' ? turn.job.subject.turnId : '', { body: 'Только про вторую книгу' },
      { idempotencyKey: createIdempotencyKey('steerChatTurn') });
    await expect(refused).rejects.toMatchObject({ status, code });
    if (mayReturn !== undefined) await expect(refused).rejects.toMatchObject({ problem: { mayReturn } });
  });

  it('stops the reading at a reason as the server does, and «Повторить» lets it go on', async () => {
    const api = await theAccount();
    await api.setReadingConsent(saga, { state: 'allowed' });
    api.setScenario({ ...api.getScenario(), readingStops: 'characters' });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'paused', pause: 'characters' });
    api.setScenario({ ...api.getScenario(), readingStops: undefined });
    // The pause is the state of the consent: it stays once the scenario is gone.
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'paused', pause: 'characters' });
    await api.setReadingConsent(saga, { state: 'allowed' });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'reading', pause: null });
  });

  it('estimates what is left: no chapter, the names and the threads waiting, the work ending first', async () => {
    const api = await theAccount({
      readingEstimateLeft: { chapters: 0, namesWait: true, threadsWait: false },
      readingLimits: { characters: { used: 3_200_000, limit: 40_000_000 }, work: { used: 319_500_000, limit: 320_000_000 } },
    });
    const estimate = await api.getReadingEstimate(saga);
    expect(estimate).toMatchObject({ chapters: 0, namesWait: true, threadsWait: false, account: { characters: 0, endsFirst: 'work' } });
    expect(estimate.account!.work).toBeGreaterThan(0);
    api.setScenario({ ...api.getScenario(), readingEstimateLeft: { chapters: 0 } });
    expect(await api.getReadingEstimate(saga)).toMatchObject({ chapters: 0, namesWait: false, threadsWait: false, account: { characters: 0, work: 0, endsFirst: null } });
  });
});
