// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';
import type { MockScenarioControl } from '../scenarios';

/** The computer of the app with its local author signed in and a saga of his linked with the example vault. */
async function linkedSaga(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  const saga = await api.createProject({ title: 'Северный предел' });
  await api.beginObsidianVaultChoice(saga.id);
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  return { api, saga: saga.id };
}

// specs/2026-11-15-desk-obsidian-export.md, checklist step 6: the mock answers the export as the scenario says.
describe('the export to Obsidian in the mock', () => {
  it('creates the references once, keeps the folder on the link and changes nothing the next time', async () => {
    const { api, saga } = await linkedSaga();
    const first = await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' });
    expect(first).toMatchObject({ folder: 'CanonKeeper/Северный предел', complete: true, created: { count: 7 }, unchanged: { count: 0 } });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' })).toEqual(first);
    const second = await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0002' });
    expect(second).toMatchObject({ created: { count: 0 }, unchanged: { count: 7 } });
    expect(await api.getObsidianVault(saga)).toMatchObject({ exportDir: 'CanonKeeper/Северный предел', lastExportAt: second.at });
  });

  it('keeps what the author changed, and moves it into _Удалено/ by his word', async () => {
    const { api, saga } = await linkedSaga();
    await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' });
    api.setScenario({ ...api.getScenario(), obsidianExport: 'changed' });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0002' })).toMatchObject({ kept: { count: 1, names: ['Герои/Сава.md'] } });
    expect(await api.exportObsidianSaga(saga, { replaceChanged: true }, { idempotencyKey: 'idem-export-0003' })).toMatchObject({
      moved: { names: ['_Удалено/Сава.md'] }, updated: { names: ['Герои/Сава.md'] }, kept: { count: 0 },
    });
  });

  it('says every refusal of Data and keeps no answer of a run that did not end', async () => {
    for (const [scenario, code, reason] of [
      ['updating', 'obsidian.map_not_ready', 'updating'], ['no_cards', 'obsidian.map_not_ready', 'no_cards'],
      ['unsafe', 'obsidian.export_dir_unsafe', undefined], ['manifest_unreadable', 'obsidian.export_manifest_unreadable', undefined],
    ] as const) {
      const { api, saga } = await linkedSaga({ obsidianExport: scenario });
      await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' }))
        .rejects.toMatchObject({ status: 409, problem: reason ? { code, reason } : { code } });
    }
    const { api, saga } = await linkedSaga({ obsidianExport: 'partial' });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' })).toMatchObject({ complete: false });
    expect((await api.getObsidianVault(saga)).lastExportAt).toBeUndefined();
    api.setScenario({ ...api.getScenario(), obsidianExport: undefined });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-0001' })).toMatchObject({ complete: true });
    // The body as the server reads it: the one optional word of the author, a boolean.
    for (const body of [{ other: true }, { replaceChanged: 'yes' }, [], null]) {
      await expect(api.exportObsidianSaga(saga, body as never, { idempotencyKey: 'idem-export-0009' }))
        .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    }
    const busy = await linkedSaga({ obsidianVault: 'busy' });
    await expect(busy.api.exportObsidianSaga(busy.saga, {}, { idempotencyKey: 'idem-export-0001' })).rejects.toMatchObject({ status: 429 });
  });
});
