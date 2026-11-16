// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { bytesOf } from '../../../shared/files/zip';
import { createMockApiClient } from '../index';
import type { MockScenarioControl } from '../scenarios';

const key = (number: number) => `idem-export-${String(number).padStart(4, '0')}`;

/**
 * The computer of the app with its local author signed in and a saga of his the helper has read — a saga opened from a saga
 * file, whose chapters brought their cards (specs/2026-11-16-desk-obsidian-export-web.md XW4.2) — linked with the example vault.
 */
async function sagaWithCards(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  const made = await api.createProject({ title: 'Северный предел' });
  const archive = await api.getSagaArchive(made.id);
  const opened = await api.openSagaArchive(new File([(await bytesOf(archive.blob)).slice()], archive.fileName), { idempotencyKey: 'idem-export-file' });
  await api.beginObsidianVaultChoice(opened.projectId);
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  return { api, saga: opened.projectId, folder: `CanonKeeper/${opened.name}`, other: made.id };
}

// specs/2026-11-15-desk-obsidian-export.md, checklist step 6; specs/2026-11-16-desk-obsidian-export-web.md XW4.1: the mock answers
// the export as the server would, in its order.
describe('the export to Obsidian in the mock', () => {
  it('exports a saga with cards: the references once, the folder kept on the link, nothing changed the next time', async () => {
    const { api, saga, folder } = await sagaWithCards();
    expect((await api.getSagaMap(saga, { sourceView: 'published' })).coverage.withCards).toBeGreaterThan(0);
    const first = await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) });
    expect(first).toMatchObject({ folder, complete: true, created: { count: 7 }, unchanged: { count: 0 }, skipped: [] });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).toEqual(first);
    const second = await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(2) });
    expect(second).toMatchObject({ created: { count: 0 }, unchanged: { count: 7 } });
    expect(await api.getObsidianVault(saga)).toMatchObject({ exportDir: folder, lastExportAt: second.at });
  });

  it('keeps what the author changed, moves it into _Удалено/ by his word, and refuses one key for both words', async () => {
    const { api, saga } = await sagaWithCards();
    await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) });
    api.setScenario({ ...api.getScenario(), obsidianExport: 'changed' });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(2) })).toMatchObject({ kept: { count: 1, names: ['Герои/Сава.md'] } });
    await expect(api.exportObsidianSaga(saga, { replaceChanged: true }, { idempotencyKey: key(2) }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'idempotency.conflict' } });
    expect(await api.exportObsidianSaga(saga, { replaceChanged: true }, { idempotencyKey: key(3) })).toMatchObject({
      moved: { names: ['_Удалено/Сава.md'] }, updated: { names: ['Герои/Сава.md'] }, kept: { count: 0 },
    });
    // «Оставить изменённое» is `{}` and `{replaceChanged: false}` alike.
    expect(await api.exportObsidianSaga(saga, { replaceChanged: false }, { idempotencyKey: key(2) })).toMatchObject({ kept: { count: 1 } });
  });

  it('decides the map by its own getSagaMap: no cards, the scenario without cards, the map being updated', async () => {
    // A saga without books: no card — `no_cards`, without any scenario.
    const { api, other } = await sagaWithCards();
    await api.beginObsidianVaultChoice(other);
    expect((await api.getSagaMap(other, { sourceView: 'published' })).coverage).toEqual({ chapters: 0, withCards: 0 });
    await expect(api.exportObsidianSaga(other, {}, { idempotencyKey: key(1) }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.map_not_ready', reason: 'no_cards' } });

    const unread = await sagaWithCards({ obsidianExport: 'no_cards' });
    const map = await unread.api.getSagaMap(unread.saga, { sourceView: 'published' });
    expect(map.coverage).toMatchObject({ withCards: 0 });
    expect(map.coverage.chapters).toBeGreaterThan(0);
    await expect(unread.api.exportObsidianSaga(unread.saga, {}, { idempotencyKey: key(1) }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.map_not_ready', reason: 'no_cards' } });

    const updating = await sagaWithCards({ obsidianExport: 'updating' });
    expect(await updating.api.getSagaMap(updating.saga, { sourceView: 'published' })).toMatchObject({ building: true });
    await expect(updating.api.exportObsidianSaga(updating.saga, {}, { idempotencyKey: key(1) }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.map_not_ready', reason: 'updating' } });
    updating.api.setScenario({ ...updating.api.getScenario(), obsidianExport: undefined });
    expect((await updating.api.getSagaMap(updating.saga, { sourceView: 'published' })).building).toBeUndefined();
    expect(await updating.api.exportObsidianSaga(updating.saga, {}, { idempotencyKey: key(1) })).toMatchObject({ complete: true });
  });

  it('says every refusal of the folder and keeps no answer of a run that did not end', async () => {
    for (const [scenario, code] of [['unsafe', 'obsidian.export_dir_unsafe'], ['manifest_unreadable', 'obsidian.export_manifest_unreadable']] as const) {
      const { api, saga } = await sagaWithCards({ obsidianExport: scenario });
      await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).rejects.toMatchObject({ status: 409, problem: { code } });
    }
    const { api, saga } = await sagaWithCards({ obsidianExport: 'partial' });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).toMatchObject({ complete: false, created: { count: 3 } });
    expect((await api.getObsidianVault(saga)).lastExportAt).toBeUndefined();
    api.setScenario({ ...api.getScenario(), obsidianExport: undefined });
    expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).toMatchObject({ complete: true });

    // `skipped`: four files of the folder not read, one by each reason, in the order of the server — and the run did not end.
    const skipped = await sagaWithCards({ obsidianExport: 'skipped' });
    const answer = await skipped.api.exportObsidianSaga(skipped.saga, {}, { idempotencyKey: key(1) });
    expect(answer).toMatchObject({ complete: false, created: { count: 3 } });
    expect(answer.skipped.map((skip) => [skip.reason, skip.count])).toEqual([['busy', 1], ['cloud_only', 1], ['link', 1], ['unreadable', 1]]);
    expect((await skipped.api.getObsidianVault(skipped.saga)).lastExportAt).toBeUndefined();
  });

  it('goes in the order of the server: the body, the saga and the role, the link, busy, the answer of the key, the map, the vault, the folder', async () => {
    const { api, saga, other } = await sagaWithCards();
    // The key and the body first: a key of 7 or 129 signs, any other field, a word that is not a boolean — in every branch.
    for (const scenario of [undefined, 'partial', 'updating', 'unsafe'] as const) {
      api.setScenario({ ...api.getScenario(), obsidianExport: scenario });
      for (const idempotencyKey of ['short-7', 'k'.repeat(129)]) {
        await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey })).rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
      }
    }
    api.setScenario({ ...api.getScenario(), obsidianExport: undefined });
    for (const body of [{ other: true }, { replaceChanged: 'yes' }, [], null]) {
      await expect(api.exportObsidianSaga('no-such-saga', body as never, { idempotencyKey: key(9) }))
        .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    }
    // The saga and the role before the link; the link before busy.
    await expect(api.exportObsidianSaga('no-such-saga', {}, { idempotencyKey: key(9) })).rejects.toMatchObject({ status: 404 });
    await api.unlinkObsidianVault(other);
    api.setScenario({ ...api.getScenario(), obsidianVault: 'busy' });
    await expect(api.exportObsidianSaga(other, {}, { idempotencyKey: key(9) })).rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.not_linked' } });
    api.setScenario({ ...api.getScenario(), obsidianVault: undefined, actorRole: 'viewer' });
    await expect(api.exportObsidianSaga(other, {}, { idempotencyKey: key(9) })).rejects.toMatchObject({ status: 403 });
    api.setScenario({ ...api.getScenario(), actorRole: undefined });

    // Busy before the answer of the key; the answer of the key before the map, the vault and the folder.
    const done = await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) });
    api.setScenario({ ...api.getScenario(), obsidianVault: 'busy' });
    await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).rejects.toMatchObject({ status: 429, problem: { code: 'obsidian.busy' } });
    for (const [vault, exported] of [[undefined, 'updating'], ['slow', undefined], [undefined, 'unsafe']] as const) {
      api.setScenario({ ...api.getScenario(), obsidianVault: vault, obsidianExport: exported });
      expect(await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) })).toEqual(done);
    }
    // The map before the vault; the vault before the folder of the export.
    api.setScenario({ ...api.getScenario(), obsidianVault: 'slow', obsidianExport: 'updating' });
    await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: key(2) })).rejects.toMatchObject({ problem: { code: 'obsidian.map_not_ready' } });
    api.setScenario({ ...api.getScenario(), obsidianVault: { missing: 'not_found' }, obsidianExport: 'unsafe' });
    await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: key(2) })).rejects.toMatchObject({ problem: { code: 'obsidian.vault_missing' } });
    api.setScenario({ ...api.getScenario(), obsidianVault: 'slow', obsidianExport: 'manifest_unreadable' });
    await expect(api.exportObsidianSaga(saga, {}, { idempotencyKey: key(2) })).rejects.toMatchObject({ status: 503, problem: { code: 'obsidian.slow' } });
  });

  it('keeps the folder of the export on a change of the path, and the time of the last one only on the same path', async () => {
    const { api, saga, folder } = await sagaWithCards();
    const done = await api.exportObsidianSaga(saga, {}, { idempotencyKey: key(1) });
    await api.beginObsidianVaultChoice(saga);
    expect(await api.getObsidianVault(saga)).toMatchObject({ exportDir: folder, lastExportAt: done.at });
    api.setScenario({ ...api.getScenario(), obsidianShell: { picked: '/home/author/Другая папка' } });
    await api.beginObsidianVaultChoice(saga);
    const moved = await api.getObsidianVault(saga);
    expect(moved).toMatchObject({ path: '/home/author/Другая папка', exportDir: folder });
    expect(moved.lastExportAt).toBeUndefined();
    // The unlink forgets both: a new link has no folder of the export.
    await api.unlinkObsidianVault(saga);
    api.setScenario({ ...api.getScenario(), obsidianShell: undefined });
    await api.beginObsidianVaultChoice(saga);
    expect((await api.getObsidianVault(saga)).exportDir).toBeUndefined();
  });
});
