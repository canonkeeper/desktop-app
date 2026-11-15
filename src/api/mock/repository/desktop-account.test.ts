// SPDX-License-Identifier: AGPL-3.0-only
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OperationRequest } from '../../../shared/api';
import { createMockApiClient } from '../index';
import type { MockScenarioControl } from '../scenarios';

/** The computer of the app with its local author signed in (specs/2026-11-12-desk-window.md WN4.1). */
async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

type Api = Awaited<ReturnType<typeof theApp>>;

const profile: NonNullable<OperationRequest<'updateAiSettings'>['own']> = {
  preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat',
  advanced: { contextWindow: 64_000, maxOutput: 16_000, reasoning: 'high', parallel: 2, priceInPerMillion: 0.27, priceOutPerMillion: 1.1, currency: 'RUB' },
};

/** The return of the browser the mock has not: the scenario of one read ends the waiting. */
async function returned(api: Api, outcome: NonNullable<MockScenarioControl['accountLinkOutcome']>) {
  api.setScenario({ ...api.getScenario(), accountLinkOutcome: outcome });
  const state = await api.getAccount();
  api.setScenario({ ...api.getScenario(), accountLinkOutcome: undefined });
  return state;
}

afterEach(() => {
  vi.useRealTimers();
});

// specs/2026-11-15-desk-account-link-web.md, checklist step 2: the mock answers every state of the link the pages draw.
describe('the link of the mock with an account', () => {
  it('waits until the scenario returns it: linked, or failed for the exchange, the keyring or the term', async () => {
    for (const outcome of ['exchange', 'keyring', 'expired'] as const) {
      const api = await theApp();
      await api.startAccountLink({ chooseHelper: true });
      expect((await api.getAccount()).state).toBe('waiting');
      expect(await returned(api, outcome)).toMatchObject({ state: 'failed', failure: outcome, link: null, account: null });
      // The failure stays until a new link starts (AW1.6).
      expect((await api.getAccount()).failure).toBe(outcome);
      expect((await api.startAccountLink()).state).toBe('waiting');
    }
    const api = await theApp();
    await api.startAccountLink();
    expect(await returned(api, 'linked')).toMatchObject({ state: 'linked', account: { email: 'a…@example.test' } });
  });

  it('ends a waiting by its term as failed expired, and one the author canceled as none', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-11-15T12:00:00Z'));
    const api = await theApp();
    await api.startAccountLink();
    vi.setSystemTime(new Date('2026-11-15T12:09:59Z'));
    expect((await api.getAccount()).state).toBe('waiting');
    vi.setSystemTime(new Date('2026-11-15T12:10:00Z'));
    expect(await api.getAccount()).toMatchObject({ state: 'failed', failure: 'expired' });
    await api.startAccountLink();
    await api.cancelAccountLink();
    expect(await api.getAccount()).toMatchObject({ state: 'none', failure: null });
  });

  it('chooses the helper of the account by the intent, or when none is chosen; «Свой ИИ» without it stays, its profile in every case', async () => {
    const intent = await theApp();
    await intent.updateAiSettings({ source: 'own', own: profile });
    await intent.startAccountLink({ chooseHelper: true });
    await returned(intent, 'linked');
    const chosen = await intent.getAiSettings();
    expect(chosen.settings.source).toBe('account');
    expect(chosen.settings.own).toMatchObject({ preset: 'deepseek', chatModel: 'deepseek-chat', advanced: { contextWindow: 64_000, currency: 'RUB' } });

    const kept = await theApp();
    await kept.updateAiSettings({ source: 'own', own: profile });
    await kept.startAccountLink({ chooseHelper: false });
    await returned(kept, 'linked');
    expect((await kept.getAiSettings()).settings.source).toBe('own');

    const fresh = await theApp();
    await fresh.startAccountLink();
    await returned(fresh, 'linked');
    expect((await fresh.getAiSettings()).settings.source).toBe('account');
    // The helper is connected: the reading of a saga does not wait for it any more.
    expect(fresh.getScenario().helperMissing).toBe(false);
  });

  it('answers a broken link, no connection with and without what the site told, the file of the pass, no questions and a term of the tier', async () => {
    expect((await (await theApp({ accountBroken: true })).getAccount()).state).toBe('broken');
    const offline = await theApp({ accountLinked: true, accountUnreachable: true });
    expect(await offline.getAccount()).toMatchObject({ state: 'linked', unreachable: true, account: { name: 'Автор Севера' } });
    const empty = await theApp({ accountLinked: true, accountUnreachable: true, accountInfoMissing: true });
    expect(await empty.getAccount()).toMatchObject({ state: 'linked', unreachable: true, account: null });
    const told = await theApp({ accountLinked: true, accountPassStore: 'file', accountQuestionsUnknown: true, accountTierUntil: '2026-12-14T00:00:00.000Z' });
    expect(await told.getAccount()).toMatchObject({
      passStore: 'file', unreachable: false, account: { questions: null, tier: { title: 'Писатель', until: '2026-12-14T00:00:00.000Z' } },
    });
  });

  it('unlinks on the computer when the site is silent, and the scenario does not link it again', async () => {
    const api = await theApp({ accountLinked: true, unlinkSiteSilent: true });
    expect((await api.getAccount()).state).toBe('linked');
    expect(await api.unlinkAccount()).toEqual({ siteUnlinked: false, passRemoved: true });
    expect((await api.getAccount()).state).toBe('none');
    const heard = await theApp({ accountLinked: true });
    expect(await heard.unlinkAccount()).toEqual({ siteUnlinked: true, passRemoved: true });
  });

  it('names the site in the network while the link waits and once linked, as the backend does', async () => {
    const api = await theApp();
    expect((await api.getDesktopState()).network).toEqual([]);
    await api.startAccountLink({ chooseHelper: true });
    expect((await api.getDesktopState()).network).toEqual([{ host: 'canonkeeper.example.test', purpose: 'связь с аккаунтом: имя, тариф и остатки' }]);
    await returned(api, 'linked');
    expect((await api.getDesktopState()).network).toEqual([
      { host: 'canonkeeper.example.test', purpose: 'связь с аккаунтом: имя, тариф и остатки; вопросы помощнику и чтение книг' },
    ]);
  });

  it('refuses a body of the start that is not its object', async () => {
    const api = await theApp();
    await expect(api.startAccountLink({ chooseHelper: 'yes' } as unknown as OperationRequest<'startAccountLink'>)).rejects.toMatchObject({ status: 400 });
  });
});
