// SPDX-License-Identifier: AGPL-3.0-only
import { act, cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import type { OperationResponse } from '../../../shared/api';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import { checkedWhen } from '../updates/update-words';
import { obsidianPath } from './address';
import { markTreeOld, obsidianKeys } from './obsidian-api';
import { aBookOfItsFolder, aSagaWithCards, axeViolations, renderAt, theApp, toastsText, until, withoutShell, withShell } from './obsidian.test-helpers';
import { exportSkipWords, exportWords, words } from './words';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

type Exported = OperationResponse<'exportObsidianSaga', '200'>;

const page = (saga: string, part = false) => `/manuscript/obsidian?projectId=${saga}${part ? '&part=export' : ''}`;
const thePart = () => screen.findByRole('region', { name: exportWords.title }, { timeout: 10_000 });
const button = (part: HTMLElement) => within(part).getByRole('button', { name: /^Выгрузить (в Obsidian|снова)$/u });
const group = (names: readonly string[], count = names.length) => ({ count, names: [...names] });
const refuse = (code: string, status = 409, fields = {}, retryAfter?: number) => new MockApiError(status, code, 'exportObsidianSaga', code, { fields, ...(retryAfter ? { retryAfter } : {}) });

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

/** The part, its button pressed, and the export it started at its end. */
async function exportOnce(part: HTMLElement) {
  await until(() => expect(reasonOf(button(part))).toBeNull());
  fireEvent.click(button(part));
}

// specs/2026-11-16-desk-obsidian-export-web.md XW1.1–XW1.3: the two parts of the page «Obsidian», the part of the export before and
// after the first one, its address and its focus.
describe('the part «Карта саги в Obsidian»', () => {
  it('stands under the part of the books and records on both steps, by the levels of the headings', async () => {
    const { api, saga } = await aSagaWithCards();
    withShell();
    const view = renderAt(api, page(saga));
    const part = await thePart();
    const title = await screen.findByRole('heading', { level: 1, name: words.pageTitle });
    const books = screen.getByRole('heading', { level: 2, name: words.partTitle });
    const step = await screen.findByRole('heading', { level: 3, name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
    const exportTitle = within(part).getByRole('heading', { level: 2, name: exportWords.title });
    const follows = (first: Node, second: Node) => Boolean(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING);
    expect([follows(title, books), follows(books, step), follows(step, exportTitle)]).toEqual([true, true, true]);
    expect(await axeViolations(view.container)).toEqual([]);

    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    const preview = await screen.findByRole('heading', { level: 3, name: words.previewTitle }, { timeout: 10_000 });
    expect(follows(screen.getByRole('heading', { level: 2, name: words.partTitle }), preview)).toBe(true);
    expect(follows(preview, within(await thePart()).getByRole('heading', { level: 2, name: exportTitle.textContent! }))).toBe(true);
  }, 20_000);

  it('stands right under the line of the folder, its button closed, when the folder is not in its place; open while it is checked', async () => {
    const { api, saga } = await aSagaWithCards({ obsidianVault: { missing: 'not_found' } });
    withShell();
    renderAt(api, page(saga));
    const part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBe(words.missing));
    expect(screen.queryByRole('heading', { name: words.partTitle })).toBeNull();
    const line = screen.getByRole('region', { name: words.folderName('Северный предел') });
    expect(Boolean(line.compareDocumentPosition(part) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
    cleanup();

    const checking = await aSagaWithCards({ obsidianVault: 'unknown' });
    renderAt(checking.api, page(checking.saga));
    const open = await thePart();
    await until(() => expect(screen.getByText(words.checking)).toBeTruthy());
    await until(() => expect(reasonOf(button(open))).toBeNull());
  });

  it('says what it writes before the first export, the last one after it, and the folder without time', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    renderAt(api, page(saga));
    const part = await thePart();
    expect(within(part).getByText(exportWords.text)).toBeTruthy();
    expect(within(part).getByText(exportWords.edits)).toBeTruthy();
    expect(button(part).textContent).toBe(exportWords.first);
    await exportOnce(part);
    await until(() => expect(button(part).textContent).toBe(exportWords.again));
    const { lastExportAt } = await api.getObsidianVault(saga);
    expect(within(part).getByText(exportWords.last(checkedWhen(lastExportAt!), folder))).toBeTruthy();
    expect(within(part).queryByText(exportWords.text)).toBeNull();
    expect(within(part).getByText(exportWords.edits)).toBeTruthy();
    cleanup();

    // A first export that did not end: the folder is known, the time of an export that ended is not.
    const partial = await aSagaWithCards({ obsidianExport: 'partial' });
    renderAt(partial.api, page(partial.saga));
    const unfinished = await thePart();
    await exportOnce(unfinished);
    await until(() => expect(within(unfinished).getByText(exportWords.folder(partial.folder))).toBeTruthy());
    expect(button(unfinished).textContent).toBe(exportWords.again);
    expect(within(unfinished).queryByText(exportWords.text)).toBeNull();
    expect(within(unfinished).queryByText(/^Последняя выгрузка/u)).toBeNull();
  });

  it('opens at its address with the focus on its heading once it stands — after the look of the folder above it', async () => {
    const { api, saga } = await aSagaWithCards();
    const look = deferred<void>();
    const real = api.scanObsidianVault.bind(api);
    vi.spyOn(api, 'scanObsidianVault').mockImplementation(async (projectId) => {
      await look.promise;
      return real(projectId);
    });
    renderAt(api, page(saga, true));
    const part = await thePart();
    const heading = within(part).getByRole('heading', { level: 2, name: exportWords.title });
    await act(() => new Promise((done) => setTimeout(done, 100)));
    expect(document.activeElement).not.toBe(heading);
    await act(async () => look.resolve());
    await until(() => expect(document.activeElement).toBe(heading));
    expect(screen.getByRole('button', { name: words.show })).toBeTruthy();
  });
});

// XW2.1–XW2.3: the run of the export from the cache of the mutations and the one fence of the bridge.
describe('the run of the export', () => {
  it('goes on for a page that comes back, its button closed, and ends there', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    const run = deferred<Exported>();
    const real = api.exportObsidianSaga.bind(api);
    const exports = vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      await run.promise;
      return real(...args);
    });
    const view = renderAt(api, page(saga));
    await exportOnce(await thePart());
    expect(await screen.findByText(exportWords.working)).toBeTruthy();
    expect(screen.getByText(exportWords.leave)).toBeTruthy();
    expect(reasonOf(button(await thePart()))).toBe(exportWords.going);
    expect(exports.mock.calls[0]![1]).toEqual({});
    view.unmount();

    renderAt(api, page(saga), { client: view.client });
    const back = await thePart();
    expect(await within(back).findByText(exportWords.working, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(reasonOf(button(back))).toBe(exportWords.going);
    await act(async () => run.resolve({} as Exported));
    expect(await within(back).findByText(exportWords.done(folder), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(exports).toHaveBeenCalledTimes(1);
  });

  it('holds the step of the choice while it goes: no look of its own, its buttons and «Сменить папку…» closed, a line in the question', async () => {
    const { api, saga } = await aSagaWithCards();
    withShell();
    const run = deferred<Exported>();
    const real = api.exportObsidianSaga.bind(api);
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      await run.promise;
      return real(...args);
    });
    const scans = vi.spyOn(api, 'scanObsidianVault');
    const view = renderAt(api, page(saga));
    await screen.findByRole('button', { name: words.show }, { timeout: 10_000 });
    await exportOnce(await thePart());
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.show }))).toBe(exportWords.going));
    expect(reasonOf(screen.getByRole('button', { name: words.rescan }))).toBe(exportWords.going);
    const line = screen.getByRole('region', { name: words.folderName('Северный предел') });
    expect(reasonOf(within(line).getByRole('button', { name: words.change }))).toBe(exportWords.going);
    fireEvent.click(within(line).getByRole('button', { name: words.folderActions }));
    fireEvent.click(await screen.findByRole('menuitem', { name: words.unlinkItem }));
    const question = await screen.findByRole('dialog', { name: words.unlinkTitle('Северный предел') });
    expect(within(question).getByText(exportWords.unlinkGoing)).toBeTruthy();
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));

    // The look made old while the export goes is not read by itself on the entry to the choice: after the export it is.
    view.unmount();
    act(() => markTreeOld(view.client, saga));
    const before = scans.mock.calls.length;
    renderAt(api, page(saga), { client: view.client });
    await thePart();
    await act(() => new Promise((done) => setTimeout(done, 200)));
    expect(scans.mock.calls.length).toBe(before);
    await act(async () => run.resolve({} as Exported));
    await until(() => expect(scans.mock.calls.length).toBe(before + 1));
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.show }))).toBeNull());
  }, 20_000);

  it('waits for a look of the folder, a preview and a write of «Загрузить» of the saga, by their words', async () => {
    const { api, saga } = await aSagaWithCards();
    const look = deferred<void>();
    const realScan = api.scanObsidianVault.bind(api);
    vi.spyOn(api, 'scanObsidianVault').mockImplementation(async (projectId) => {
      await look.promise;
      return realScan(projectId);
    });
    renderAt(api, page(saga));
    const part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBe(words.scanning));
    await act(async () => look.resolve());
    await until(() => expect(reasonOf(button(part))).toBeNull());

    const reading = deferred<void>();
    const realPreview = api.previewObsidianImport.bind(api);
    vi.spyOn(api, 'previewObsidianImport').mockImplementation(async (...args) => {
      await reading.promise;
      return realPreview(...args);
    });
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    await until(() => expect(reasonOf(button(part))).toBe(words.whyReading));
    await act(async () => reading.resolve());

    const writing = deferred<void>();
    const realRecords = api.importObsidianMaterials.bind(api);
    vi.spyOn(api, 'importObsidianMaterials').mockImplementation(async (...args) => {
      await writing.promise;
      return realRecords(...args);
    });
    fireEvent.click(await screen.findByRole('button', { name: words.load }, { timeout: 10_000 }));
    await until(() => expect(reasonOf(button(part))).toBe(words.loadGoing));
    await act(async () => writing.resolve());
    await until(() => expect(reasonOf(button(part))).toBeNull());
  }, 20_000);
});

// XW2.4–XW2.7: the result — the summary, the groups and their names, the question «Заменить изменённые…», the files that did not
// read, a run that did not end; the ways back.
describe('the result of the export', () => {
  it('sums up a complete export, its groups folded but those the author must know, the names by their paths', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    const heroes = Array.from({ length: 124 }, (_, index) => `Герои/Герой ${index + 1}.md`);
    vi.spyOn(api, 'exportObsidianSaga').mockResolvedValue({
      folder, at: new Date().toISOString(), complete: true, created: group(heroes.slice(0, 50), 124),
      updated: group(['Главы/Последний свет/1 Пепел.md'], 14), unchanged: group([], 602), moved: group(['_Удалено/Призрак.md'], 3), kept: group([]), skipped: [],
    });
    const view = renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    expect(await within(part).findByText(exportWords.done(folder))).toBeTruthy();
    expect(within(part).getByText('Создано 124 · обновлено 14 · без изменений 602 · убрано в «_Удалено» 3')).toBeTruthy();
    const groupOf = (label: string) => within(part).getByText(label).closest('details')!;
    for (const label of ['Созданы: 124', 'Обновлены: 14', 'Убраны в «_Удалено»: 3']) expect(groupOf(label).open, label).toBe(false);
    expect(within(groupOf('Созданы: 124')).getByText(/^Герои\/Герой 1, Герои\/Герой 2, .* и ещё 74$/u)).toBeTruthy();
    expect(within(groupOf('Обновлены: 14')).getByText('Главы/Последний свет/1 Пепел и ещё 13')).toBeTruthy();
    expect(within(part).getByText(exportWords.movedNote).closest('details')).toBeNull();
    expect(within(part).queryByText(/^Без изменений/u)).toBeNull();
    expect(within(part).queryByText(new RegExp(exportWords.kept, 'u'))).toBeNull();
    expect(await axeViolations(view.container)).toEqual([]);
  });

  it('asks before it replaces what the author changed, and replaces it by a new export with his word', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-before' });
    api.setScenario({ ...api.getScenario(), obsidianExport: 'changed' });
    const exports = vi.spyOn(api, 'exportObsidianSaga');
    const view = renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    const kept = (await within(part).findByText(exportWords.group(exportWords.kept, 1))).closest('details')!;
    expect(kept.open).toBe(true);
    expect(within(kept).getByText('Герои/Сава')).toBeTruthy();
    fireEvent.click(within(kept).getByRole('button', { name: exportWords.replace }));
    let question = await screen.findByRole('dialog', { name: exportWords.questionTitle });
    expect(within(question).getByText(exportWords.question(1, folder))).toBeTruthy();
    expect(await axeViolations(view.container.ownerDocument.body)).toEqual([]);
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    expect(exports).toHaveBeenCalledTimes(1);

    fireEvent.click(within(part).getByRole('button', { name: exportWords.replace }));
    question = await screen.findByRole('dialog', { name: exportWords.questionTitle });
    fireEvent.click(within(question).getByRole('button', { name: exportWords.confirm }));
    expect(await within(part).findByText(exportWords.group(exportWords.moved, 1), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(exports).toHaveBeenCalledTimes(2);
    expect(exports.mock.calls[1]![1]).toEqual({ replaceChanged: true });
    expect(exports.mock.calls[1]![2].idempotencyKey).not.toBe(exports.mock.calls[0]![2].idempotencyKey);
    expect(within(part).queryByText(exportWords.group(exportWords.kept, 1))).toBeNull();
  });

  it('says a run that did not end over what it wrote, the files that did not read by their reasons, and keeps the last one that ended', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    const ended = await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-before' });
    api.setScenario({ ...api.getScenario(), obsidianExport: 'skipped' });
    renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    const warning = await within(part).findByText(exportWords.partial);
    expect(warning.closest('[data-tone="warning"]')).toBeTruthy();
    expect(within(part).queryByText(exportWords.done(folder))).toBeNull();
    expect(within(part).getByText('Без изменений 3')).toBeTruthy();
    const skipped = within(part).getByText(exportWords.group(exportWords.skipped, 4)).closest('details')!;
    expect(skipped.open).toBe(true);
    // By the table of the reasons (Data), not by the order of the answer.
    const lines = [...skipped.querySelectorAll('p')].map((line) => line.textContent);
    expect(lines.filter((line) => Object.values(exportSkipWords).includes(line!))).toEqual(Object.values(exportSkipWords));
    expect(within(skipped).getByText('Герои/Мара')).toBeTruthy();
    expect(within(part).getByText(exportWords.last(checkedWhen(ended.at), folder))).toBeTruthy();
    // One button: the button of the part; the groups open by their summaries.
    expect(within(part).getAllByRole('button').map((each) => each.textContent)).toEqual([exportWords.again]);
  });

  it('repeats a failure and a vault read by another with the same key and body', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    const real = api.exportObsidianSaga.bind(api);
    let failures = 1;
    const exports = vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      if (failures-- > 0) throw refuse('server.error', 500);
      return real(...args);
    });
    renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    const failed = await within(part).findByRole('alert');
    fireEvent.click(within(failed).getByRole('button', { name: words.retry }));
    expect(await within(part).findByText(exportWords.done(folder), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(exports.mock.calls.map((call) => [call[1], call[2].idempotencyKey])).toEqual([[{}, exports.mock.calls[0]![2].idempotencyKey], [{}, exports.mock.calls[0]![2].idempotencyKey]]);
    cleanup();

    const held = await aSagaWithCards();
    const busy = vi.spyOn(held.api, 'exportObsidianSaga').mockRejectedValue(refuse('obsidian.busy', 429, {}, 0.01));
    renderAt(held.api, page(held.saga));
    const waiting = await thePart();
    await exportOnce(waiting);
    expect(await within(waiting).findByText(words.busyEnd, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(busy).toHaveBeenCalledTimes(6);
    fireEvent.click(within(waiting).getByRole('button', { name: words.retry }));
    await until(() => expect(busy).toHaveBeenCalledTimes(7));
    expect(new Set(busy.mock.calls.map((call) => call[2].idempotencyKey)).size).toBe(1);
  }, 15_000);

  it('is closed by the unlink of the folder and by its change', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    withShell();
    renderAt(api, page(saga));
    let part = await thePart();
    await exportOnce(part);
    expect(await within(part).findByText(exportWords.done(folder))).toBeTruthy();
    api.setScenario({ ...api.getScenario(), obsidianShell: { picked: '/home/author/Другая папка' } });
    fireEvent.click(within(screen.getByRole('region', { name: words.folderName('Северный предел') })).getByRole('button', { name: words.change }));
    await screen.findByRole('region', { name: words.folderName('Другая папка') }, { timeout: 10_000 });
    part = await thePart();
    await until(() => expect(within(part).queryByText(exportWords.done(folder))).toBeNull());

    await exportOnce(part);
    expect(await within(part).findByText(exportWords.done(folder))).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: words.folderActions }));
    fireEvent.click(await screen.findByRole('menuitem', { name: words.unlinkItem }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: words.unlinkTitle('Северный предел') })).getByRole('button', { name: words.unlink }));
    api.setScenario({ ...api.getScenario(), obsidianShell: undefined });
    fireEvent.click(await screen.findByRole('button', { name: words.choose }, { timeout: 10_000 }));
    part = await thePart();
    expect(within(part).queryByText(exportWords.done(folder))).toBeNull();
    expect(within(part).getByText(exportWords.text)).toBeTruthy();
  }, 20_000);
});

// XW3.1, XW3.2: the button waits for the map by the answer of the map; every refusal of the export at its part.
describe('the refusals of the export', () => {
  it('waits for a map being updated and opens by itself when it is ready; a refusal of the map reads the map again', async () => {
    const { api, saga } = await aSagaWithCards({ obsidianExport: 'updating' });
    renderAt(api, page(saga));
    const part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBe(exportWords.updating));
    api.setScenario({ ...api.getScenario(), obsidianExport: undefined });
    await until(() => expect(reasonOf(button(part))).toBeNull());
    // The cache of the map says ready, the server says it is not: the refusal reads the map, and the map leads the button.
    api.setScenario({ ...api.getScenario(), obsidianExport: 'updating' });
    fireEvent.click(button(part));
    await until(() => expect(reasonOf(button(part))).toBe(exportWords.updating));
    expect(within(part).queryByRole('alert')).toBeNull();
  }, 20_000);

  it('waits for the books of a saga without them, and for the helper with the line of the reading under the part', async () => {
    const empty = await theApp();
    await empty.api.beginObsidianVaultChoice(empty.saga);
    renderAt(empty.api, page(empty.saga));
    let part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBe(exportWords.noBooks));
    expect(within(part).queryByText(/Помощник ещё не подключён/u)).toBeNull();
    cleanup();

    const unread = await aBookOfItsFolder();
    renderAt(unread.api, page(unread.saga));
    part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBe(exportWords.noCards));
    expect(await within(part).findByText('Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас', undefined, { timeout: 10_000 })).toBeTruthy();
  }, 20_000);

  it('says each refusal of the folder of the export by its words, the folder named once the page knows it', async () => {
    const unsafe = await aSagaWithCards({ obsidianExport: 'unsafe' });
    renderAt(unsafe.api, page(unsafe.saga));
    let part = await thePart();
    await exportOnce(part);
    expect(await within(part).findByText(exportWords.unsafeNoName)).toBeTruthy();
    cleanup();

    for (const [scenario, said] of [['unsafe', exportWords.unsafe], ['manifest_unreadable', exportWords.manifest]] as const) {
      const { api, saga, folder } = await aSagaWithCards();
      await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-before' });
      api.setScenario({ ...api.getScenario(), obsidianExport: scenario });
      renderAt(api, page(saga));
      part = await thePart();
      await exportOnce(part);
      const alert = await within(part).findByRole('alert');
      expect(alert.textContent).toBe(said(folder));
      expect(within(alert).queryByRole('button')).toBeNull();
      cleanup();
    }

    const slow = await aSagaWithCards();
    renderAt(slow.api, page(slow.saga));
    part = await thePart();
    await until(() => expect(reasonOf(button(part))).toBeNull());
    slow.api.setScenario({ ...slow.api.getScenario(), obsidianVault: 'slow' });
    fireEvent.click(button(part));
    expect((await within(part).findByRole('alert')).textContent).toBe(exportWords.slow);
  }, 30_000);

  it('says a folder gone, a link gone and a refusal of too many by the words of the page, with no way out of its own', async () => {
    for (const [error, said] of [
      [refuse('obsidian.vault_missing', 409, { vaultReason: 'not_folder' }), words.notFolderNow],
      [refuse('rate_limit.exceeded', 429), words.rate],
    ] as const) {
      const { api, saga } = await aSagaWithCards();
      vi.spyOn(api, 'exportObsidianSaga').mockRejectedValue(error);
      const reads = vi.spyOn(api, 'getObsidianVault');
      renderAt(api, page(saga));
      const part = await thePart();
      await exportOnce(part);
      const alert = await within(part).findByRole('alert');
      expect(alert.textContent).toBe(said);
      expect(within(alert).queryByRole('button')).toBeNull();
      if (error.code === 'obsidian.vault_missing') await until(() => expect(reads.mock.calls.length).toBeGreaterThan(1));
      cleanup();
    }

    const { api, saga } = await aSagaWithCards();
    const real = api.exportObsidianSaga.bind(api);
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      await api.unlinkObsidianVault(saga);
      return real(...args);
    });
    withShell();
    renderAt(api, page(saga));
    await exportOnce(await thePart());
    expect(await screen.findByRole('button', { name: words.choose }, { timeout: 10_000 })).toBeTruthy();
    expect(screen.queryByRole('region', { name: exportWords.title })).toBeNull();
  }, 20_000);
});

// XW2.8: the line of the end of an export in the area of the toasts — silent on the page of the bridge of its saga where the part
// stands, said elsewhere, «не закончилась» of a refusal; «Открыть итог» leads to the part.
describe('the line of the end of an export on another page', () => {
  it('is silent on the page of the saga, says the end elsewhere and opens the result', async () => {
    const { api, saga } = await aSagaWithCards();
    const view = renderAt(api, page(saga), { notices: true });
    await exportOnce(await thePart());
    expect(await within(await thePart()).findByText(/^Выгружено в папку/u)).toBeTruthy();
    expect(toastsText()).not.toContain('Выгрузка в Obsidian');

    const run = deferred<void>();
    const real = api.exportObsidianSaga.bind(api);
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      await run.promise;
      return real(...args);
    });
    fireEvent.click(button(await thePart()));
    await screen.findByText(exportWords.working);
    act(() => view.go(`/manuscript/books?projectId=${saga}`));
    await act(async () => run.resolve());
    await until(() => expect(toastsText()).toContain('Выгрузка в Obsidian закончена: без изменений 7'));
    fireEvent.click(screen.getByRole('button', { name: words.openResult }));
    expect(view.steps.at(-1)).toMatchObject({ path: obsidianPath, query: { projectId: saga, part: 'export' } });
  }, 20_000);

  it('says «не закончилась» of a refusal, and not an export that ended before it was there', async () => {
    const { api, saga } = await aSagaWithCards();
    vi.spyOn(api, 'exportObsidianSaga').mockRejectedValue(refuse('obsidian.export_dir_unsafe'));
    const view = renderAt(api, page(saga));
    await exportOnce(await thePart());
    await within(await thePart()).findByRole('alert');
    view.unmount();
    // The observer comes after the export ended: it says nothing of it.
    const again = renderAt(api, `/manuscript/books?projectId=${saga}`, { client: view.client, notices: true });
    await screen.findAllByRole('button', { name: words.from }, { timeout: 10_000 });
    expect(toastsText()).not.toContain('Выгрузка в Obsidian');
    act(() => again.go(page(saga)));
    fireEvent.click(button(await thePart()));
    await within(await thePart()).findByRole('alert');
    act(() => again.go(`/manuscript/books?projectId=${saga}`));
    expect(toastsText()).not.toContain('Выгрузка в Obsidian');
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async () => {
      await new Promise((done) => setTimeout(done, 50));
      throw refuse('obsidian.export_dir_unsafe');
    });
    act(() => again.go(page(saga)));
    fireEvent.click(button(await thePart()));
    act(() => again.go(`/manuscript/books?projectId=${saga}`));
    await until(() => expect(toastsText()).toContain(exportWords.notEnded));
  }, 20_000);
});

/** An export of the saga held until the test lets it end, its answer the mock's. */
function holdExports(api: Awaited<ReturnType<typeof aSagaWithCards>>['api']) {
  const run = deferred<void>();
  const real = api.exportObsidianSaga.bind(api);
  vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
    await run.promise;
    return real(...args);
  });
  return run;
}

// The fixes of the two reviews of the slice (16.11): the one fence on every button that starts a work of the bridge (XW2.3), the
// question «Заменить изменённые…» by the reasons of the part (XW2.5), the note of «_Удалено» under its folded group (XW2.4), the
// result of an export that went at the unlink (XW2.4), the end of an export heard (XW2.2).
describe('the one fence and the result, after the reviews', () => {
  it('closes the ways out of the notes of the step of the choice while the export goes: a cut look, a look that failed', async () => {
    const cut = await aSagaWithCards({ obsidianVault: 'truncated' });
    withShell();
    const run = holdExports(cut.api);
    renderAt(cut.api, page(cut.saga));
    const part = await thePart();
    const note = (await screen.findByText(words.cut, undefined, { timeout: 10_000 })).closest('[data-tone]') as HTMLElement;
    await exportOnce(part);
    await until(() => expect(reasonOf(within(note).getByRole('button', { name: words.change }))).toBe(exportWords.going));
    await act(async () => run.resolve());
    await until(() => expect(reasonOf(within(note).getByRole('button', { name: words.change }))).toBeNull());
    cleanup();

    const failed = await aSagaWithCards();
    vi.spyOn(failed.api, 'scanObsidianVault').mockRejectedValue(new MockApiError(500, 'failure', 'scanObsidianVault', 'server.error'));
    const held = holdExports(failed.api);
    renderAt(failed.api, page(failed.saga));
    const block = await screen.findByRole('alert', { name: words.scanFailed }, { timeout: 10_000 });
    await exportOnce(await thePart());
    await until(() => expect(reasonOf(within(block).getByRole('button', { name: words.retry }))).toBe(exportWords.going));
    await act(async () => held.resolve());
  }, 20_000);

  it('closes the step «что получится» while the export goes: «Загрузить», «Показать заново», the link of a card', async () => {
    const { api, saga } = await aSagaWithCards();
    await api.createBook(saga, { title: 'Застава' });
    const run = holdExports(api);
    const view = renderAt(api, page(saga));
    const part = await thePart();
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    const link = await screen.findByRole('button', { name: words.linkSimilar('Застава') }, { timeout: 10_000 });
    act(() => view.client.setQueryData(obsidianKeys.preview(saga), (preview: { booksChanged: boolean } | undefined) => (preview ? { ...preview, booksChanged: true } : preview)));
    await exportOnce(part);
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.load }))).toBe(words.whyStale));
    expect(reasonOf(screen.getByRole('button', { name: words.showAgain }))).toBe(exportWords.going);
    expect(reasonOf(link)).toBe(exportWords.going);
    act(() => view.client.setQueryData(obsidianKeys.preview(saga), (preview: { booksChanged: boolean } | undefined) => (preview ? { ...preview, booksChanged: false } : preview)));
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.load }))).toBe(exportWords.going));
    await act(async () => run.resolve());
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.load }))).toBeNull());
  }, 20_000);

  it('asks «Заменить изменённые…» only when the part may export, and keeps the list of the edited notes on a refusal of the map', async () => {
    const { api, saga } = await aSagaWithCards();
    await api.exportObsidianSaga(saga, {}, { idempotencyKey: 'idem-export-before' });
    api.setScenario({ ...api.getScenario(), obsidianExport: 'changed' });
    const view = renderAt(api, page(saga));
    const part = await thePart();
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    await screen.findByRole('button', { name: words.load }, { timeout: 10_000 });
    await exportOnce(part);
    const replace = await within(part).findByRole('button', { name: exportWords.replace }, { timeout: 10_000 });
    expect(reasonOf(replace)).toBeNull();
    // «Загрузить» holds the vault: the question waits with the words of the part.
    const writing = deferred<void>();
    const realRecords = api.importObsidianMaterials.bind(api);
    vi.spyOn(api, 'importObsidianMaterials').mockImplementation(async (...args) => {
      await writing.promise;
      return realRecords(...args);
    });
    fireEvent.click(screen.getByRole('button', { name: words.load }));
    await until(() => expect(reasonOf(within(part).getByRole('button', { name: exportWords.replace }))).toBe(words.loadGoing));
    await act(async () => writing.resolve());
    await until(() => expect(reasonOf(within(part).getByRole('button', { name: exportWords.replace }))).toBeNull());
    // The map says ready in the cache, the server says it is being updated: the list stays, the button says why it waits.
    api.setScenario({ ...api.getScenario(), obsidianExport: 'updating' });
    fireEvent.click(within(part).getByRole('button', { name: exportWords.replace }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: exportWords.questionTitle })).getByRole('button', { name: exportWords.confirm }));
    await until(() => expect(reasonOf(button(part))).toBe(exportWords.updating));
    expect(within(part).getByText(exportWords.group(exportWords.kept, 1))).toBeTruthy();
    expect(reasonOf(within(part).getByRole('button', { name: exportWords.replace }))).toBe(exportWords.updating);
    expect(view.client.getMutationCache().findAll({ mutationKey: obsidianKeys.export }).length).toBeGreaterThan(1);
  }, 20_000);

  it('says the note of «_Удалено» under the folded group, not inside it', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    vi.spyOn(api, 'exportObsidianSaga').mockResolvedValue({
      folder, at: new Date().toISOString(), complete: true, created: group([]), updated: group([]), unchanged: group([], 6),
      moved: group(['_Удалено/Призрак.md'], 1), kept: group([]), skipped: [],
    });
    renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    const note = await within(part).findByText(exportWords.movedNote);
    const moved = within(part).getByText(exportWords.group(exportWords.moved, 1)).closest('details')!;
    expect(moved.open).toBe(false);
    expect(note.closest('details')).toBeNull();
    expect(Boolean(moved.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
  });

  it('closes the result of an export that went at the unlink once it ends: a new link shows none', async () => {
    const { api, saga } = await aSagaWithCards();
    withShell();
    const real = api.exportObsidianSaga.bind(api);
    const hold = deferred<void>();
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async (...args) => {
      const answer = await real(...args);
      await hold.promise;
      return { ...answer, complete: false };
    });
    renderAt(api, page(saga));
    const part = await thePart();
    await exportOnce(part);
    await within(part).findByText(exportWords.working);
    fireEvent.click(screen.getByRole('button', { name: words.folderActions }));
    fireEvent.click(await screen.findByRole('menuitem', { name: words.unlinkItem }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: words.unlinkTitle('Северный предел') })).getByRole('button', { name: words.unlink }));
    fireEvent.click(await screen.findByRole('button', { name: words.choose }, { timeout: 10_000 }));
    const again = await thePart();
    await act(async () => hold.resolve());
    await until(() => expect(within(again).queryByText(exportWords.working)).toBeNull());
    expect(within(again).queryByText(exportWords.partial)).toBeNull();
    expect(within(again).queryByText(/^Создано/u)).toBeNull();
  }, 20_000);

  it('waits in the window «Связать с папкой Obsidian» and in the block of a folder not in its place while the export goes', async () => {
    const { api, saga } = await aSagaWithCards({ obsidianLinked: true });
    withShell();
    const run = holdExports(api);
    const view = renderAt(api, page(saga));
    const part = await thePart();
    const decisions = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    await exportOnce(part);
    await within(part).findByText(exportWords.working);
    fireEvent.click(within(decisions).getAllByRole('button', { name: words.linkOther })[0]!);
    const window = await screen.findByRole('dialog', { name: words.linkTitle });
    expect(reasonOf(within(window).getByRole('button', { name: words.doLink }))).toBe(exportWords.going);
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    api.setScenario({ ...api.getScenario(), obsidianVault: { missing: 'not_found' } });
    await act(() => view.client.invalidateQueries({ queryKey: obsidianKeys.vault(saga) }));
    const missing = (await screen.findByText(words.missing)).closest('[data-tone]') as HTMLElement;
    expect(reasonOf(within(missing).getByRole('button', { name: words.change }))).toBe(exportWords.going);
    await act(async () => run.resolve());
  }, 20_000);

  it('says the first refusal «папку записать нельзя» by the folder the server kept for it, not first without a name', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    let kept = false;
    const realVault = api.getObsidianVault.bind(api);
    // The read of the folder takes its time, as over a network.
    vi.spyOn(api, 'getObsidianVault').mockImplementation(async (projectId) => {
      const vault = await realVault(projectId);
      if (!kept) return vault;
      await new Promise((done) => setTimeout(done, 50));
      return { ...vault, exportDir: folder };
    });
    vi.spyOn(api, 'exportObsidianSaga').mockImplementation(async () => {
      kept = true;
      throw refuse('obsidian.export_dir_unsafe');
    });
    renderAt(api, page(saga));
    const part = await thePart();
    // Every word the alert of the part says, as it changes: a screen reader hears each.
    const said = new Set<string>();
    const watch = new MutationObserver(() => {
      const alert = part.querySelector('[role="alert"]');
      if (alert?.textContent) said.add(alert.textContent);
    });
    watch.observe(part, { childList: true, subtree: true, characterData: true });
    await exportOnce(part);
    expect((await within(part).findByRole('alert')).textContent).toBe(exportWords.unsafe(folder));
    await act(() => new Promise((done) => setTimeout(done, 100)));
    watch.disconnect();
    expect([...said]).toEqual([exportWords.unsafe(folder)]);
  });

  it('says the end of the export in the line of its state: the run, then the folder it went to or that it did not end', async () => {
    const { api, saga, folder } = await aSagaWithCards();
    const run = holdExports(api);
    renderAt(api, page(saga));
    const part = await thePart();
    const state = within(part).getByRole('status', { name: exportWords.title });
    await exportOnce(part);
    await until(() => expect(state.textContent).toContain(exportWords.working));
    await act(async () => run.resolve());
    await until(() => expect(state.textContent).toBe(exportWords.done(folder)));
    api.setScenario({ ...api.getScenario(), obsidianExport: 'partial' });
    vi.mocked(api.exportObsidianSaga).mockRestore();
    fireEvent.click(button(part));
    await until(() => expect(state.textContent).toBe(exportWords.partial));
  });

  it('shows the line of the reading under the part as the line of «Книги» does', async () => {
    const unread = await aBookOfItsFolder();
    renderAt(unread.api, page(unread.saga));
    const part = await thePart();
    const line = await within(part).findByRole('region', { name: 'Чтение саги' }, { timeout: 10_000 });
    expect(within(line).getByText('Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас')).toBeTruthy();
  });
});

