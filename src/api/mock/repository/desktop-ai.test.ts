// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
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
type Own = NonNullable<OperationRequest<'updateAiSettings'>['own']>;

async function own(api: Api, settings: Own) {
  return api.updateAiSettings({ source: 'own', own: settings });
}

/** The reads of a check until it ends: the probes the page sees at each read. */
async function readCheck(api: Api) {
  const seen: number[] = [];
  for (let read = 0; read < 20; read += 1) {
    const check = await api.getAiCheck();
    seen.push(check.probes.length);
    if (check.state !== 'running') return { check, seen };
  }
  throw new Error('the check of the mock never ended');
}

// specs/2026-11-13-desk-helper-web.md, checklist step 2: the mock answers every state the part «Помощник» draws.
describe('«Свой ИИ» of the mock', () => {
  it('keeps the key in the store of the system, or in a file without one', async () => {
    expect((await (await theApp()).getAiSettings()).secretsStore).toBe('system');
    expect((await (await theApp({ aiSecretsStore: 'file' })).getAiSettings()).secretsStore).toBe('file');
  });

  it('refuses a key the shell did not keep, and keeps nothing', async () => {
    const api = await theApp();
    await expect(api.setAiKey({ key: 'key-test-not-kept-0001' })).rejects.toMatchObject({ status: 503, code: 'ai.key_not_saved' });
    expect((await api.getAiSettings()).keySaved).toBe(false);
    await api.setAiKey({ key: 'key-test-0001' });
    expect((await api.getAiSettings()).keySaved).toBe(true);
  });

  // specs/2026-11-14-desk-journal.md JR4.1: the network names the provider of «Свой ИИ» while its address is kept, an address of
  // this computer as a program of this computer, as the backend does.
  it('names the provider of «Свой ИИ» in the network', async () => {
    const api = await theApp();
    expect((await api.getDesktopState()).network).toEqual([]);
    await own(api, { preset: 'custom', baseUrl: 'https://llm.example.test/v1' });
    expect((await api.getDesktopState()).network).toEqual([{ host: 'llm.example.test', purpose: 'помощник через «Свой ИИ»' }]);
    await own(api, { preset: 'ollama' });
    expect((await api.getDesktopState()).network).toEqual([{ host: '127.0.0.1:11434', purpose: 'программа на этом компьютере, помощник через «Свой ИИ»' }]);
  });

  it('says every outcome of the list of models', async () => {
    const api = await theApp();
    await own(api, { preset: 'deepseek' });
    await expect(api.listAiModels()).rejects.toMatchObject({ status: 409, code: 'ai.not_set' });
    await api.setAiKey({ key: 'key-test-0001' });
    expect(await api.listAiModels()).toEqual({ outcome: 'listed', models: ['deepseek-chat', 'deepseek-reasoner'], reason: null });
    await own(api, { preset: 'custom', baseUrl: 'https://llm.example.test/v1' });
    expect(await api.listAiModels()).toEqual({ outcome: 'unlisted', models: [], reason: null });
    for (const [host, reason] of [['down', 'unreachable'], ['busy', 'rate_limited'], ['broken', 'other']] as const) {
      await own(api, { preset: 'custom', baseUrl: `https://${host}.example.test/v1` });
      expect(await api.listAiModels()).toEqual({ outcome: 'failed', models: [], reason });
    }
    await api.setAiKey({ key: 'key-test-refused-0001' });
    await own(api, { preset: 'custom', baseUrl: 'https://llm.example.test/v1' });
    expect(await api.listAiModels()).toEqual({ outcome: 'failed', models: [], reason: 'key_refused' });
  });

  it('runs a check probe by probe and keeps its report', async () => {
    const api = await theApp();
    await api.setAiKey({ key: 'key-test-0001' });
    await own(api, { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' });
    const started = await api.startAiCheck();
    expect(started).toMatchObject({ state: 'running', probes: [], current: { probe: 'short', role: 'chat', model: 'deepseek-chat' } });
    await expect(api.startAiCheck()).rejects.toMatchObject({ status: 409, code: 'ai.check_running' });
    const { check, seen } = await readCheck(api);
    // One probe more at each read, the five of one model in both roles.
    expect(seen).toEqual([1, 2, 3, 4, 5]);
    expect(check.state).toBe('done');
    expect(check.report).toMatchObject({ chat: { fit: 'fit', reason: null }, books: { fit: 'fit', reason: null }, stale: false });
    expect((await api.getAiSettings()).check).toMatchObject({ chat: { fit: 'fit' }, books: { fit: 'fit' }, stale: false });
    // A model changed after the check: the report is «проверено до изменения».
    await own(api, { preset: 'deepseek', chatModel: 'deepseek-reasoner', cardsModel: 'deepseek-chat' });
    expect((await api.getAiSettings()).check?.stale).toBe(true);
  });

  it('stops a check the author stops, keeping no report', async () => {
    const api = await theApp();
    await own(api, { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4' });
    await api.startAiCheck();
    await api.cancelAiCheck();
    expect(await api.getAiCheck()).toMatchObject({ state: 'canceled', current: null });
    expect((await api.getAiSettings()).check).toBeNull();
  });

  it.each([
    ['the model of the chat calls no tool', { preset: 'deepseek', chatModel: 'deepseek-reasoner', cardsModel: 'deepseek-reasoner' }, 'done', { fit: 'unfit', reason: 'no_tools' }, { fit: 'fit', reason: null }],
    ['the model sees only the start of a long text', { preset: 'ollama', chatModel: 'qwen3:8b-short-context', cardsModel: 'qwen3:8b-short-context' }, 'done', { fit: 'unfit', reason: 'start_only' }, { fit: 'unfit', reason: 'start_only' }],
    ['the model cuts a long answer', { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4-short-answer' }, 'done', { fit: 'fit', reason: null }, { fit: 'unfit', reason: 'cut_answer' }],
    ['the provider has no model of the name', { preset: 'ollama', chatModel: 'missing-model', cardsModel: 'missing-model' }, 'done', { fit: 'unfit', reason: 'no_model' }, { fit: 'unfit', reason: 'no_model' }],
    ['the provider asks to wait', { preset: 'ollama', chatModel: 'busy-model', cardsModel: 'busy-model' }, 'done', { fit: 'unfit', reason: 'rate_limited' }, { fit: 'unfit', reason: 'rate_limited' }],
    ['the provider does not answer', { preset: 'custom', baseUrl: 'https://down.example.test/v1', chatModel: 'north', cardsModel: 'north' }, 'done', { fit: 'unfit', reason: 'unreachable' }, { fit: 'unfit', reason: 'unreachable' }],
    ['the three minutes run out on the long answer', { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4-slow' }, 'timed_out', { fit: 'fit', reason: null }, { fit: 'unfit', reason: 'slow' }],
  ] as const)('ends a check where %s', async (_case, settings, state, chat, books) => {
    const api = await theApp();
    await api.setAiKey({ key: 'key-test-0001' });
    await own(api, settings);
    await api.startAiCheck();
    const { check } = await readCheck(api);
    expect(check.state).toBe(state);
    expect(check.report).toMatchObject({ chat, books });
  });

  it.each([
    ['missing-model', 'no_model'],
    ['busy-model', 'rate_limited'],
  ] as const)('fails every probe of %s for its reason, as the gateway does (only the connection and the key stop a check)', async (model, reason) => {
    const api = await theApp();
    await own(api, { preset: 'ollama', chatModel: model, cardsModel: model });
    await api.startAiCheck();
    const { check } = await readCheck(api);
    expect(check.probes.map((probe) => [probe.probe, probe.passed, probe.reason])).toEqual(
      ['short', 'tool', 'long_input', 'calibrate', 'long_output'].map((probe) => [probe, false, reason]),
    );
  });

  it('ends a check whose key the provider refused at its first probe', async () => {
    const api = await theApp();
    await api.setAiKey({ key: 'key-test-refused-0001' });
    await own(api, { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' });
    await api.startAiCheck();
    const { check, seen } = await readCheck(api);
    expect(seen).toEqual([1]);
    expect(check.probes).toEqual([{ probe: 'short', role: 'chat', model: 'deepseek-chat', passed: false, reason: 'key_refused' }]);
    expect(check.report).toMatchObject({ chat: { fit: 'unfit', reason: 'key_refused' }, books: { fit: 'unfit', reason: 'key_refused' } });
  });

  it('says a new window waits for the answer of the helper while one runs', async () => {
    const api = await theApp({ aiAnswerRunning: true });
    await own(api, { preset: 'ollama' });
    expect((await own(api, { preset: 'ollama', advanced: { contextWindow: 64_000 } })).appliesAfterAnswer).toBe(true);
    api.setScenario({ ...api.getScenario(), aiAnswerRunning: false });
    expect((await api.getAiSettings()).appliesAfterAnswer).toBe(false);
  });

  it('connects the helper of the app by the one rule: the reading then waits for the consent of the author', async () => {
    const api = await theApp();
    expect(api.getScenario()).toMatchObject({ helperMissing: true });
    await own(api, { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' });
    expect(api.getScenario()).toMatchObject({ helperMissing: true });
    await api.setAiKey({ key: 'key-test-0001' });
    expect(api.getScenario()).toMatchObject({ helperMissing: false, consentMissing: true });
    await api.deleteAiKey();
    expect(api.getScenario()).toMatchObject({ helperMissing: true, consentMissing: false });
  });
});

// specs/2026-11-13-desk-account-ai.md AI1.1, AI1.2, AI1.6: the helper of the account is chosen only by a linked app whose site
// answers; it has no check and no list of models; it is connected while the app is linked.
describe('the helper of the account of the mock', () => {
  it('is refused to an app that is not linked and to a site that does not answer', async () => {
    const api = await theApp();
    await expect(api.updateAiSettings({ source: 'account' })).rejects.toMatchObject({ code: 'helper.account_unlinked' });
    const silent = await theApp({ accountLinked: true, accountSilent: true });
    await expect(silent.updateAiSettings({ source: 'account' })).rejects.toMatchObject({ code: 'ai.account_unreachable' });
    expect((await silent.getAiSettings()).settings.source).toBe('none');
  });

  it('takes the helper the site tells, without a check and without models', async () => {
    const api = await theApp({ accountLinked: true });
    const state = await api.updateAiSettings({ source: 'account' });
    expect(state.settings.source).toBe('account');
    expect(state.account).toEqual({ chatModel: 'gpt-north-chat', cardsModel: 'gpt-north-cards', contextWindow: 1_000_000, compactLimit: 950_000 });
    expect(state.check).toBeNull();
    await expect(api.startAiCheck()).rejects.toMatchObject({ code: 'helper.not_for_source' });
    await expect(api.listAiModels()).rejects.toMatchObject({ code: 'helper.not_for_source' });
    await api.unlinkAccount();
    expect((await api.getAiSettings()).settings.source).toBe('account');
  });
});
