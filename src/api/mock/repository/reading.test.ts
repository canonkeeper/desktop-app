// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';

const saga = 'project-white-port';

async function connected(api: ReturnType<typeof createMockApiClient>, prices: boolean) {
  api.setScenario({ preset: 'normal', actorRole: 'owner' });
  await api.updateAiSettings({
    source: 'own',
    own: {
      preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-reasoner',
      advanced: prices ? { currency: 'RUB', priceInPerMillion: 30, priceOutPerMillion: 120 } : {},
    },
  });
}

// specs/2026-11-13-desk-consent.md CN4.2: the mock answers the operations of the reading and the status in every state of the
// table CN4.1 — the window of the consent with and without prices, the sample going, done, stopped and failed, the reading
// going, paused for each reason, with the chapters read and the names waiting, at another provider, with chapters that did
// not read — so the pages are drawn by them.
describe('the mock of the reading of the books by «Свой ИИ»', () => {
  it('answers the window of the consent with prices and without', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    const priced = await api.getReadingEstimate(saga);
    expect(priced).toMatchObject({ unit: 'RUB', kind: 'rough', host: 'api.deepseek.com', local: false, sampleChapters: 3 });
    expect(priced.amount).toBeGreaterThan(0);
    expect(priced.suggestedStop).toBeGreaterThan(priced.amount!);
    await connected(api, false);
    expect(await api.getReadingEstimate(saga)).toMatchObject({ unit: 'tokens', amount: null });
    expect((await api.getIndexingStatus(saga)).reading ?? null).toMatchObject({ state: 'waiting_consent', reason: null, unit: 'tokens' });
  });

  it('answers the sample going, done, stopped and failed', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    expect((await api.getReadingSample(saga)).state).toBe('none');
    expect((await api.startReadingSample(saga)).state).toBe('queued');
    const going = await api.getReadingSample(saga);
    expect(going).toMatchObject({ state: 'running', done: 1, total: 3 });
    await api.getReadingSample(saga);
    const done = await api.getReadingSample(saga);
    expect(done).toMatchObject({ state: 'succeeded', done: 3, total: 3, spent: { unit: 'RUB', approximate: true } });
    expect(done.chapters[0]).toMatchObject({ place: expect.stringContaining('Книга 1'), summary: expect.any(String) });
    expect(done.seconds).toBeGreaterThan(0);

    await api.startReadingSample(saga);
    await api.getReadingSample(saga);
    await api.cancelReadingSample(saga);
    expect(await api.getReadingSample(saga)).toMatchObject({ state: 'canceled', done: 1 });

    await api.startReadingSample(saga);
    api.setScenario({ preset: 'normal', actorRole: 'owner', sampleFailure: 'funds' });
    expect(await api.getReadingSample(saga)).toMatchObject({ state: 'failed', failure: 'funds' });
  });

  it('answers the reading going, paused for each reason, read with the names waiting, at another provider, with chapters that did not read', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    const given = await api.setReadingConsent(saga, { state: 'allowed', stopAt: 1_000_000_000 });
    expect(given).toMatchObject({ state: 'allowed', host: 'api.deepseek.com', unit: 'RUB', stopAt: 1_000_000_000, step: 1_000_000_000 });
    const going = await api.getIndexingStatus(saga);
    expect(going.reading ?? null).toMatchObject({ state: 'reading', unit: 'RUB', stopAt: 1_000_000_000 });

    for (const pause of ['author', 'sum', 'funds', 'key', 'model', 'connection', 'no_answer', 'prices'] as const) {
      api.setScenario({ preset: 'normal', actorRole: 'owner', readingPause: pause });
      const status = await api.getIndexingStatus(saga);
      expect(status.reading).toMatchObject({ state: 'paused', pause, stopAt: 1_000_000_000, step: 1_000_000_000 });
      expect(await api.getReadingConsent(saga)).toMatchObject({ state: 'paused', pause });
    }

    api.setScenario({ preset: 'normal', actorRole: 'owner', readingPause: 'sum', readingNamesWait: true });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'paused', namesWait: true, threadsWait: true });

    api.setScenario({ preset: 'normal', actorRole: 'owner', readingUnread: 1, readingNamesWait: false });
    const continued = await api.setReadingConsent(saga, { state: 'allowed', continueBy: 500_000_000 });
    expect(continued).toMatchObject({ state: 'allowed', step: 500_000_000 });

    expect((await api.getIndexingStatus(saga)).reading ?? null).toMatchObject({ state: 'done', unread: { chapters: 1, reason: 'cut_answer' } });
    api.setScenario({ preset: 'normal', actorRole: 'owner', readingUnread: 2, readingUnreadReason: 'unreadable', readingNamesWait: false });
    expect((await api.getIndexingStatus(saga)).reading ?? null).toMatchObject({ state: 'done', unread: { chapters: 2, reason: 'unreadable' } });
    // specs/2026-11-15-desk-reading-no-answer.md NA2.2: the chapters the provider did not take.
    api.setScenario({ preset: 'normal', actorRole: 'owner', readingUnread: 3, readingUnreadReason: 'refused', readingNamesWait: false });
    expect((await api.getIndexingStatus(saga)).reading ?? null).toMatchObject({ state: 'done', unread: { chapters: 3, reason: 'refused' } });

    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    await api.updateAiSettings({ source: 'own', own: { preset: 'openai', chatModel: 'north-large', cardsModel: 'north-small' } });
    expect(await api.getReadingConsent(saga)).toMatchObject({ state: 'none', reason: 'provider_changed' });
    expect((await api.getIndexingStatus(saga)).reading ?? null).toMatchObject({ state: 'waiting_consent', reason: 'provider_changed' });
    await expect(api.setReadingConsent(saga, { state: 'paused' })).rejects.toMatchObject({ status: 409, code: 'reading.no_consent' });
  });

  // specs/2026-11-13-desk-consent-web.md, the trace (the mock): without a consent the cards Job reads nothing; under one it
  // reads a chapter a step, paid from the consent, and the reading pauses at «сумма» before a chapter that would pass the sum;
  // «Продолжить ещё на X» reads on to the end.
  it('reads under the consent a chapter a step, paid, and pauses at the sum', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    const step = async () => {
      const jobs = (await api.listProjectJobs(saga, { limit: 50 })).data
        .filter((job) => job.kind === 'chapter_cards' && (job.status === 'queued' || job.status === 'running'));
      for (const job of jobs) await api.getJob(job.id);
      return (await api.getIndexingStatus(saga)).reading ?? null;
    };
    const waiting = await step();
    expect(waiting).toMatchObject({ state: 'waiting_consent' });
    const before = waiting!.chaptersRead;
    expect(await step()).toMatchObject({ state: 'waiting_consent', chaptersRead: before });

    // A chapter costs 1,3 ₽ here: a sum of 1 ₽ pauses before it; «ещё на 2 ₽» reads it, the last one of the saga.
    await api.setReadingConsent(saga, { state: 'allowed', stopAt: 1_000_000 });
    let reading = (await api.getIndexingStatus(saga)).reading!;
    expect(reading).toMatchObject({ state: 'reading', spent: 0, stopAt: 1_000_000, chaptersRead: before });
    for (let at = 0; at < 4 && reading.state === 'reading'; at += 1) reading = (await step())!;
    expect(reading).toMatchObject({ state: 'paused', pause: 'sum', chaptersRead: before, spent: 0, step: 1_000_000 });

    await api.setReadingConsent(saga, { state: 'allowed', continueBy: 2_000_000 });
    reading = (await api.getIndexingStatus(saga)).reading!;
    expect(reading).toMatchObject({ state: 'reading', stopAt: 2_000_000, chaptersTotal: before + 1 });
    let after: typeof reading | null = reading;
    for (let at = 0; at < 4 && after; at += 1) after = await step();
    // Every chapter is read: the status says no reading, and the consent counts the chapter.
    expect(after).toBeNull();
    expect(await api.getReadingConsent(saga)).toMatchObject({ state: 'allowed', spent: { amount: 1_300_000, inputTokens: 16_000, outputTokens: 6_500 } });
  });

  // specs/2026-11-15-desk-reading-rest.md RR2.5: the marks are the state of the saga — the scenario only sows them; «Повторить»
  // forgets the chapters, the names and the threads at once and the status goes at once; a step of the worker of the mock brings
  // the names and the threads together, or — as the scenario sowed — marks them again.
  it('keeps the marks as the state of the saga: «Повторить» forgets them, a step of the worker brings them together or marks them again', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    await api.setReadingConsent(saga, { state: 'allowed', stopAt: null });
    const step = async () => {
      const jobs = (await api.listProjectJobs(saga, { limit: 50 })).data
        .filter((job) => job.kind === 'chapter_cards' && (job.status === 'queued' || job.status === 'running'));
      for (const job of jobs) await api.getJob(job.id);
      return (await api.getIndexingStatus(saga)).reading ?? null;
    };
    api.setScenario({ preset: 'normal', actorRole: 'owner', readingUnread: 1, readingNamesUnread: 'unreadable', readingThreadsUnread: 'no_answer' });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({
      state: 'done', namesWait: false, threadsWait: false, unread: { chapters: 1 }, unreadRest: { names: 'unreadable', threads: 'no_answer' },
    });
    // The scenario sows once: the marks stay as they are when the scenario says nothing new.
    api.setScenario({ ...api.getScenario(), helperMissing: false });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'done', unreadRest: { names: 'unreadable' } });

    await api.setReadingConsent(saga, { state: 'allowed', retryUnread: true });
    const going = (await api.getIndexingStatus(saga)).reading ?? null;
    expect(going).toMatchObject({ state: 'reading', unread: null, unreadRest: null });
    let reading = going;
    for (let at = 0; at < 8 && reading; at += 1) reading = await step();
    expect(reading).toBeNull();

    // Sown to fail again: the step marks the names again with their reason.
    api.setScenario({ preset: 'normal', actorRole: 'owner', readingNamesUnread: 'refused', readingRestAgain: true });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'done', unreadRest: { names: 'refused', threads: null } });
    await api.setReadingConsent(saga, { state: 'allowed', retryUnread: true });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'reading', namesWait: true, threadsWait: false, unreadRest: null });
    reading = await step();
    for (let at = 0; at < 8 && reading?.state === 'reading'; at += 1) reading = await step();
    expect(reading).toMatchObject({ state: 'done', unreadRest: { names: 'refused', threads: null } });

    // Without a consent that acts the marks do not act: all that is marked waits, and the reading waits for the consent.
    await api.updateAiSettings({ source: 'own', own: { preset: 'openai', chatModel: 'north-large', cardsModel: 'north-small' } });
    expect((await api.getIndexingStatus(saga)).reading).toMatchObject({ state: 'waiting_consent', namesWait: true, unreadRest: null });
  });

  // specs/2026-11-13-desk-consent-web.md RW3.1: getAiSettings says how many sagas a change of the currency or the removal of
  // the prices would pause — a consent that acts with a sum in money of the currency now; the pause of the author acts, a
  // sum in tokens, no sum, another host or a sum already waiting for a new one do not.
  it('says in getAiSettings the sagas read under a sum in money', async () => {
    const api = createMockApiClient();
    await connected(api, true);
    expect((await api.getAiSettings()).moneyStopSagas).toBe(0);
    await api.setReadingConsent(saga, { state: 'allowed', stopAt: null });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(0);
    await api.setReadingConsent(saga, { state: 'allowed', stopAt: 1_000_000_000 });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(1);
    await api.setReadingConsent(saga, { state: 'paused' });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(1);
    await api.updateAiSettings({
      source: 'own', own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-reasoner', advanced: { currency: 'USD', priceInPerMillion: 0.3, priceOutPerMillion: 1.2 } },
    });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(0);
    await connected(api, false);
    await api.setReadingConsent(saga, { state: 'allowed', stopAt: 25_000_000 });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(0);
    await api.updateAiSettings({ source: 'own', own: { preset: 'openai', chatModel: 'north-large', cardsModel: 'north-small' } });
    expect((await api.getAiSettings()).moneyStopSagas).toBe(0);
  });
});
