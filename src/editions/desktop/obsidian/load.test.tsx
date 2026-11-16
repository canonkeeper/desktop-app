// SPDX-License-Identifier: AGPL-3.0-only
import { act, cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import { authorMaterialQueryKeys } from '../../../entities/author-materials/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { sagaQueryKeys } from '../../../entities/saga/api';
import { NetworkApiError } from '../../../shared/api';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import type { ChoiceMemory, FolderChoice } from './choice';
import { keepChoice } from './choice-memory';
import { obsidianKeys } from './obsidian-api';
import { axeViolations, renderAt, theApp, until, withoutShell, withShell, type TheApp } from './obsidian.test-helpers';
import { outcomeLine, words } from './words';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const nothingElse: Record<string, FolderChoice> = {
  '.': 'skip', 'Книга 1': 'skip', 'Книга 2': 'skip', 'Книга 3': 'skip', 'Книга 4': 'skip', 'Книга 5': 'skip', 'Книга 5/Герои': 'skip', Персонажи: 'skip', Места: 'skip', Шаблоны: 'skip',
};

async function chosen(kinds: Record<string, FolderChoice>, scenario: Parameters<typeof theApp>[0] = {}, notes: string[] = []) {
  const app = await theApp(scenario);
  await app.api.beginObsidianVaultChoice(app.saga);
  withShell();
  const memory: ChoiceMemory = { kinds: { ...nothingElse, ...kinds }, notes };
  keepChoice('user-local', app.saga, memory);
  return app;
}

async function previewed(api: TheApp, saga: string, options: Parameters<typeof renderAt>[2] = {}) {
  const view = renderAt(api, `/manuscript/obsidian?projectId=${saga}`, options);
  fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
  await screen.findByRole('button', { name: words.load }, { timeout: 10_000 });
  return view;
}

const load = () => fireEvent.click(screen.getByRole('button', { name: words.load }));
const plain = (text: string) => text.replace(/\s/gu, ' ');

// specs/2026-11-15-desk-obsidian-web.md OW4.1–OW4.3: one «Загрузить» — the records, then the books — with the bodies and keys of
// its preview.
describe('«Загрузить»', () => {
  it('sends the records, then the books, with the bodies of the preview: a book inside a folder of records, an excluded note', async () => {
    const { api, saga } = await chosen({
      '.': 'materials:other', 'Книга 1': 'book', 'Книга 3': 'book', Места: 'materials:place',
      'Книга 2': 'together', 'Книга 4': 'together', 'Книга 5': 'together', 'Книга 5/Герои': 'together', Персонажи: 'together', Шаблоны: 'together',
    }, { obsidianLinked: true }, ['Места/Застава.md']);
    const records = vi.spyOn(api, 'importObsidianMaterials');
    const books = vi.spyOn(api, 'importObsidianBooks');
    const asked = vi.spyOn(api, 'previewObsidianImport');
    await previewed(api, saga);
    const preview = await asked.mock.results[0]!.value as Awaited<ReturnType<TheApp['previewObsidianImport']>>;
    expect(asked.mock.calls[0]![1]).toEqual({
      books: ['Книга 1', 'Книга 3'], materials: [{ path: '.', kind: 'other' }, { path: 'Места', kind: 'place' }], exclude: ['Места/Застава.md'],
    });
    load();
    await until(() => expect(books).toHaveBeenCalled());
    expect(records.mock.invocationCallOrder[0]!).toBeLessThan(books.mock.invocationCallOrder[0]!);
    const print = (folder: string) => preview.materials.folders.find((one) => one.folder === folder)!.fingerprint;
    expect(records.mock.calls[0]![1]).toEqual({
      books: ['Книга 1', 'Книга 3'],
      materials: [{ path: '.', kind: 'other', fingerprint: print('.') }, { path: 'Места', kind: 'place', fingerprint: print('Места') }],
      exclude: ['Места/Застава.md'],
      records: preview.materials.records.map((record) => ({ path: record.path, seen: record.seen })),
      replace: [],
    });
    expect(records.mock.calls[0]![1].records!.some((record) => record.path === 'Места/Застава.md')).toBe(false);
    expect(books.mock.calls[0]![1]).toEqual({
      books: [{ folder: 'Книга 3', fingerprint: preview.books.find((book) => book.folder === 'Книга 3')!.fingerprint }],
      materials: [{ path: '.', kind: 'other' }, { path: 'Места', kind: 'place' }],
      exclude: ['Места/Застава.md'],
    });
  });

  it('asks the folder of a book it loaded by the composition of the load — the memory of the browser clean or kept: no change, its records there', async () => {
    const { api, saga } = await chosen({ 'Книга 5': 'book', 'Книга 5/Герои': 'materials:character', 'Книга 5/Часть 2/Черновики': 'skip' });
    const asked = vi.spyOn(api, 'previewObsidianImport');
    await previewed(api, saga);
    expect(asked.mock.calls[0]![1]).toEqual({ books: ['Книга 5'], materials: [{ path: 'Книга 5/Герои', kind: 'character' }], exclude: ['Книга 5/Часть 2/Черновики'] });
    load();
    expect(await screen.findByText(words.bookQueued('Долгая зима'), undefined, { timeout: 10_000 })).toBeTruthy();
    const queue = (await api.listImportQueues(saga)).data[0]!;
    await api.getJob(queue.items[0]!.importJobId!);
    const kept = window.localStorage.getItem(`canonkeeper.obsidian.choice.user-local.${saga}`);
    for (const memory of [null, kept]) {
      cleanup();
      window.localStorage.clear();
      if (memory) window.localStorage.setItem(`canonkeeper.obsidian.choice.user-local.${saga}`, memory);
      asked.mockClear();
      renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
      fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
      const book = await screen.findByRole('listitem', { name: 'Долгая зима' }, { timeout: 10_000 });
      const body = asked.mock.calls[0]![1];
      expect(body.books).toContain('Книга 5');
      expect(body.materials).toContainEqual({ path: 'Книга 5/Герои', kind: 'character' });
      expect(body.exclude).toContain('Книга 5/Часть 2/Черновики');
      expect(within(book).getByText(words.stSame('Долгая зима'))).toBeTruthy();
      const records = await screen.findByRole('list', { name: words.records });
      for (const name of ['Тим', 'Сава']) expect(within(within(records).getByText(name).closest('li')!).getByText(words.rSame)).toBeTruthy();
    }
  }, 30_000);

  it('sends the decisions of the records and the rules: a decision of a record older than the rule', async () => {
    const { api, saga } = await chosen({ Персонажи: 'materials:character', Места: 'materials:place' }, { obsidianLinked: true });
    const real = api.previewObsidianImport.bind(api);
    vi.spyOn(api, 'previewObsidianImport').mockImplementation(async (projectId, body) => {
      const answer = await real(projectId, body);
      const extra = { ...answer.materials.records.find((record) => record.state === 'edited_in_app')!, path: 'Персонажи/Ирвин.md', name: 'Ирвин' };
      return { ...answer, materials: { ...answer.materials, records: [...answer.materials.records, extra] } };
    });
    const records = vi.spyOn(api, 'importObsidianMaterials').mockResolvedValue({
      created: { count: 0, names: [] }, updated: { count: 0, names: [] }, replaced: { count: 0, names: [] }, unchanged: { count: 0, names: [] },
      kept: { taken: { count: 0, names: [] }, edited: { count: 0, names: [] }, changed: { count: 0, names: [] } }, skipped: [],
    });
    await previewed(api, saga);
    const forAll = screen.getByText(words.forEdited(2)).parentElement!;
    fireEvent.click(within(forAll).getByRole('button', { name: words.replace }));
    fireEvent.click(within(screen.getByRole('group', { name: 'Ирвин' })).getByRole('radio', { name: words.keep }));
    load();
    await until(() => expect(records).toHaveBeenCalled());
    const body = records.mock.calls[0]![1];
    expect(body.replace).toEqual(['edited_in_app']);
    expect(body.records!.find((record) => record.path === 'Персонажи/Ирвин.md')).toMatchObject({ decision: 'keep' });
    expect(body.records!.find((record) => record.path === 'Персонажи/Йорген.md')).not.toHaveProperty('decision');
  });

  it('sends no write without its folders: no records without a folder of records, no books without a new one', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    const records = vi.spyOn(api, 'importObsidianMaterials');
    const books = vi.spyOn(api, 'importObsidianBooks');
    await previewed(api, saga);
    load();
    await until(() => expect(books).toHaveBeenCalled());
    expect(records).not.toHaveBeenCalled();
    cleanup();
    const notes = await chosen({ Места: 'materials:place' });
    const noBooks = vi.spyOn(notes.api, 'importObsidianBooks');
    const someRecords = vi.spyOn(notes.api, 'importObsidianMaterials');
    await previewed(notes.api, notes.saga);
    load();
    await until(() => expect(someRecords).toHaveBeenCalled());
    expect(noBooks).not.toHaveBeenCalled();
  });

  it('writes by the keys of the preview: new ones a preview, the same after a lost connection, one repeat of `conflict` by itself', { timeout: 20_000 }, async () => {
    const { api, saga } = await chosen({ Места: 'materials:place' });
    const records = vi.spyOn(api, 'importObsidianMaterials').mockRejectedValueOnce(new NetworkApiError(new Error('offline')));
    const view = await previewed(api, saga);
    load();
    expect(await screen.findByText(/^Записи не сохранены: нет связи с сервером/u)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: words.retry }));
    await until(() => expect(records).toHaveBeenCalledTimes(2));
    const key = records.mock.calls[0]![2].idempotencyKey;
    expect(records.mock.calls[1]![2].idempotencyKey).toBe(key);
    expect(key).toMatch(/^importObsidianMaterials:/u);
    // «Загрузить ещё» and a new preview: new keys of its writes.
    fireEvent.click(await screen.findByRole('button', { name: words.loadMore }));
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    await screen.findByRole('button', { name: words.load }, { timeout: 10_000 });
    const next = view.client.getQueryData<{ keys: { materials: string; books: string } }>(obsidianKeys.preview(saga))!.keys;
    expect(next.materials).toMatch(/^importObsidianMaterials:/u);
    expect(next.materials).not.toBe(key);
    expect(next.books).toMatch(/^importObsidianBooks:/u);
    cleanup();

    const raced = await chosen({ Места: 'materials:place' });
    const conflicted = vi.spyOn(raced.api, 'importObsidianMaterials').mockRejectedValue(new MockApiError(409, 'raced', 'importObsidianMaterials', 'conflict'));
    await previewed(raced.api, raced.saga);
    load();
    expect(await screen.findByText(words.partRecords(words.conflict))).toBeTruthy();
    expect(conflicted).toHaveBeenCalledTimes(2);
    expect(new Set(conflicted.mock.calls.map((call) => call[2].idempotencyKey)).size).toBe(1);
  });

  it('loads the books again by a new key after `load_interrupted` and a failure after the load began; a part that failed leaves the other', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    const books = vi.spyOn(api, 'importObsidianBooks').mockRejectedValueOnce(new MockApiError(409, 'interrupted', 'importObsidianBooks', 'obsidian.load_interrupted'));
    await previewed(api, saga);
    load();
    expect(await screen.findByText(words.partBooks(words.interrupted))).toBeTruthy();
    expect(screen.getByText(/^Создано 2/u)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: words.loadBooksAgain }));
    await until(() => expect(books).toHaveBeenCalledTimes(2));
    expect(books.mock.calls[1]![2].idempotencyKey).not.toBe(books.mock.calls[0]![2].idempotencyKey);
    expect(await screen.findByText(words.bookQueued('Книга 3'))).toBeTruthy();
  });
});

// OW4.4, OW4.5: the result by the groups and outcomes, its buttons; what is read again after each write.
describe('the result of «Загрузить»', () => {
  it('says the records by the groups of the answer and the books by their outcomes, with the books of changes named', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 2': 'book', 'Книга 3': 'book', Шаблоны: 'book', Персонажи: 'materials:character', Места: 'materials:place' }, { obsidianLinked: true });
    const view = await previewed(api, saga);
    const reads = vi.spyOn(view.client, 'invalidateQueries');
    load();
    expect(await screen.findByText(words.bookQueued('Книга 3'), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(screen.getByText('Создано 4, обновлено 1, без изменений 1')).toBeTruthy();
    expect(screen.getByText(`${words.keptTaken}Йорген`)).toBeTruthy();
    expect(screen.getByText(`${words.keptEdited}Йорген`)).toBeTruthy();
    expect(screen.getByRole('button', { name: words.showAgain })).toBeTruthy();
    expect(screen.getByText(words.bookLinked('Застава'))).toBeTruthy();
    expect(screen.getByText(words.bookInTrash('Белый порт'))).toBeTruthy();
    expect(screen.getByText(words.bookRefused('Шаблоны', 'в заметках папки не нашлось текста глав'))).toBeTruthy();
    expect(screen.getByText(words.notUpdated('Застава'))).toBeTruthy();
    for (const button of [words.toBooks, words.toMaterials, words.loadMore]) expect(screen.getByRole('button', { name: button })).toBeTruthy();
    expect(await axeViolations(view.container)).toEqual([]);
    const keys = reads.mock.calls.map((call) => JSON.stringify(call[0]?.queryKey));
    for (const key of [authorMaterialQueryKeys.project(saga), sagaQueryKeys.projectMaps(saga), manuscriptQueryKeys.importQueues(saga), manuscriptQueryKeys.shelf({ projectId: saga }), obsidianKeys.vault(saga)]) {
      expect(keys).toContain(JSON.stringify(key));
    }
    fireEvent.click(screen.getByRole('button', { name: words.toMaterials }));
    expect(view.steps.at(-1)).toMatchObject({ path: '/memory', query: { projectId: saga, part: 'materials' } });
  });

  it('is seen after a leave and a return; on another page the line of the end says it with «Открыть итог»; the folder is read when its books are in', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    let answerRecords: () => void = () => undefined;
    const real = api.importObsidianMaterials.bind(api);
    vi.spyOn(api, 'importObsidianMaterials').mockImplementation((projectId, body, options) => new Promise((resolve, reject) => {
      answerRecords = () => real(projectId, body, options).then(resolve, reject);
    }));
    const view = await previewed(api, saga, { notices: true });
    load();
    // The line of the load is a status of its own (the part of the export under the step has its own, XW2.2).
    expect((await screen.findByText(words.saving)).closest('[role="status"]')).toBeTruthy();
    act(() => view.go(`/manuscript/books?projectId=${saga}`));
    const reads = vi.spyOn(view.client, 'invalidateQueries');
    answerRecords();
    const line = await screen.findByText(plain(outcomeLine({ saved: 2 }, { loading: 1 })), undefined, { timeout: 10_000 });
    fireEvent.click(within(line.closest('[role="status"], [role="alert"], li, div')!.parentElement!).getByRole('button', { name: words.openResult }));
    expect(view.steps.at(-1)).toMatchObject({ path: '/manuscript/obsidian', query: { projectId: saga, step: 'preview' } });
    expect(await screen.findByText(words.bookQueued('Книга 3'))).toBeTruthy();
    // The load of «Книги» it started ends: the folder of the saga is read again (OW4.5).
    const queue = (await api.listImportQueues(saga)).data[0]!;
    await api.getJob(queue.items[0]!.importJobId!);
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(saga) }));
    const status = await api.getIndexingStatus(saga, { bookId: (await api.getImportQueue(saga, queue.id)).items[0]!.bookId!, sourceView: 'draft' });
    const index = status.steps.find((step) => step.kind === 'text_index')!.jobId!;
    await api.getJob(index);
    await api.getJob(index);
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(saga) }));
    await until(() => expect(reads.mock.calls.filter((call) => JSON.stringify(call[0]?.queryKey) === JSON.stringify(obsidianKeys.vault(saga))).length).toBeGreaterThan(1));
  }, 20_000);

  it('keeps the result while the window is open — past the five minutes of a mutation — until «Загрузить ещё»', async () => {
    const { api, saga } = await chosen({ Места: 'materials:place' });
    // «Книги» drawn once first, so its lazy code does not suspend when the author goes there later.
    const view = renderAt(api, `/manuscript/books?projectId=${saga}`);
    await screen.findAllByRole('button', { name: words.from }, { timeout: 10_000 });
    act(() => view.go(`/manuscript/obsidian?projectId=${saga}`));
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    await screen.findByRole('button', { name: words.load }, { timeout: 10_000 });
    load();
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      act(() => view.go(`/manuscript/books?projectId=${saga}`));
      act(() => { vi.advanceTimersByTime(6 * 60 * 1000); });
    } finally {
      vi.useRealTimers();
    }
    act(() => view.go(`/manuscript/obsidian?projectId=${saga}&step=preview`));
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    // The result stands without the heading of its step: its parts are h3 under the h2 of the part (no h2 → h4).
    expect(screen.getByRole('heading', { level: 3, name: words.records })).toBeTruthy();
    expect(screen.queryByRole('button', { name: words.load })).toBeNull();
    // No heading «Что получится» over the result (board 44·53).
    expect(screen.queryByRole('heading', { name: words.previewTitle })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: words.loadMore }));
    await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
    expect(screen.queryByText(words.loadEnded)).toBeNull();
    expect(view.client.getMutationCache().getAll().filter((mutation) => mutation.state.status !== 'pending').length).toBe(0);
  }, 20_000);

  it('reads the folder of the saga at the end of the load of «Книги» it began, also once «Загрузить ещё» closed its result', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    const view = await previewed(api, saga, { notices: true });
    load();
    expect(await screen.findByText(words.bookQueued('Книга 3'), undefined, { timeout: 10_000 })).toBeTruthy();
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(saga) }));
    fireEvent.click(screen.getByRole('button', { name: words.loadMore }));
    await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
    const reads = vi.spyOn(view.client, 'invalidateQueries');
    const queue = (await api.listImportQueues(saga)).data[0]!;
    await api.getJob(queue.items[0]!.importJobId!);
    const status = await api.getIndexingStatus(saga, { bookId: (await api.getImportQueue(saga, queue.id)).items[0]!.bookId!, sourceView: 'draft' });
    const index = status.steps.find((step) => step.kind === 'text_index')!.jobId!;
    await api.getJob(index);
    await api.getJob(index);
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(saga) }));
    await until(() => expect(reads.mock.calls.some((call) => JSON.stringify(call[0]?.queryKey) === JSON.stringify(obsidianKeys.vault(saga)))).toBe(true));
  }, 20_000);

  it('says on the choice of the folders that a load goes or ended, with «Открыть итог»; the end is said by a toast unless the author is on its result', async () => {
    const { api, saga } = await chosen({ Места: 'materials:place' });
    let answerRecords: () => void = () => undefined;
    const real = api.importObsidianMaterials.bind(api);
    vi.spyOn(api, 'importObsidianMaterials').mockImplementation((projectId, body, options) => new Promise((resolve, reject) => {
      answerRecords = () => real(projectId, body, options).then(resolve, reject);
    }));
    const view = await previewed(api, saga, { notices: true });
    load();
    // The line of the load is a status of its own (the part of the export under the step has its own, XW2.2).
    expect((await screen.findByText(words.saving)).closest('[role="status"]')).toBeTruthy();
    // «Из Obsidian…» leads to the choice of the folders: it says the load that goes, and «Показать, что получится» waits for it.
    act(() => view.go(`/manuscript/obsidian?projectId=${saga}`));
    await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
    expect(await screen.findByText(words.loadGoing, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(reasonOf(screen.getByRole('button', { name: words.show }))).toBe(words.loadGoing);
    answerRecords();
    // The end while the author is on the choice of this saga: the toast says it, and so does the line of the step.
    expect(await screen.findByText(plain(outcomeLine({ saved: 2 }, null)), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(await screen.findByText(words.loadEnded)).toBeTruthy();
    const line = screen.getByText(words.loadEnded).parentElement!;
    fireEvent.click(within(line).getByRole('button', { name: words.openResult }));
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(view.steps.at(-1)).toMatchObject({ path: '/manuscript/obsidian', query: { projectId: saga, step: 'preview' } });
  }, 20_000);

  it('says «Книги сейчас не загрузить» only when the books were not sent for a live load of another — not of its own load', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    const view = await previewed(api, saga);
    load();
    expect(await screen.findByText(words.bookQueued('Книга 3'), undefined, { timeout: 10_000 })).toBeTruthy();
    // The load of «Книги» the write of the books began is live now: the page read its loads again.
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(saga) }));
    expect((await api.listImportQueues(saga)).data.some((queue) => queue.status === 'running')).toBe(true);
    expect(screen.queryByText(words.queueRunning)).toBeNull();
    expect(screen.queryByText(/^Книги сейчас не загрузить/u)).toBeNull();
    cleanup();

    // Another load of the saga was live: the books were not sent, the result says why.
    const other = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    await other.api.createImportQueue(other.saga, { items: [{ clientItemId: 'one', position: 1, sourceFileName: 'Том 2.md' }] }, { idempotencyKey: 'idem-other-live-queue' });
    const books = vi.spyOn(other.api, 'importObsidianBooks');
    await previewed(other.api, other.saga);
    load();
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(books).not.toHaveBeenCalled();
    expect(screen.getAllByText(words.queueRunning).length).toBe(1);
  });

  it.each([
    [{ records: { saved: 102 }, books: { loading: 3 } }, 'Из Obsidian: записей сохранено — 102, книг загружается — 3'],
    [{ records: { saved: 102 }, books: 'failed' }, 'Из Obsidian: записей сохранено — 102; книги не загрузились'],
    [{ records: 'failed', books: { loading: 3 } }, 'Из Obsidian: записи не сохранились; книг загружается — 3'],
  ] as const)('words the line of the end %#', (parts, said) => {
    expect(plain(outcomeLine(parts.records, parts.books))).toBe(said);
  });
});

// OW4.7: every refusal of the writes with its words and its way out, by part.
describe('the refusals of «Загрузить»', () => {
  const refuse = (operation: 'importObsidianMaterials' | 'importObsidianBooks', code: string, status: number, fields: Record<string, unknown> = {}) => new MockApiError(status, code, operation, code, { fields });
  it.each([
    ['importObsidianBooks', refuse('importObsidianBooks', 'obsidian.folder_changed', 409, { folders: ['Книга 3'] }), words.partBooks(words.folderChanged(['Книга 3'])), [words.showAgain]],
    ['importObsidianBooks', refuse('importObsidianBooks', 'obsidian.queue_busy', 409, { queue: { id: 'q', status: 'running' } }), words.partBooks(words.queueBusy), [words.toBooks]],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'obsidian.too_large', 422, { records: 5_003, sagaRecords: 5_003, limitRecords: 5_000 }), words.partRecords(`записей получится ${(5_003).toLocaleString('ru-RU')} при пределе ${(5_000).toLocaleString('ru-RU')} на сагу. Уберите часть папок записей.`), []],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'obsidian.folders_overlap', 422, { folders: ['Места', 'Места/Порты'] }), words.partRecords(words.overlap('Места', 'Места/Порты').replace(/^П/u, 'п')), [words.showAgain]],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'request.validation', 400), words.partRecords(words.validation.replace(/^Н/u, 'н')), [words.showAgain]],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'idempotency.conflict', 409), words.partRecords(words.validation.replace(/^Н/u, 'н')), [words.showAgain]],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'obsidian.slow', 503), words.partRecords(words.slow.replace(/^П/u, 'п')), [words.retry]],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'obsidian.vault_missing', 409, { vaultReason: 'not_folder' }), words.partRecords(words.notFolderNow.replace(/^П/u, 'п')), []],
    ['importObsidianMaterials', refuse('importObsidianMaterials', 'rate_limit.exceeded', 429), words.partRecords(words.rateLoad), [words.retry]],
    ['importObsidianBooks', refuse('importObsidianBooks', 'rate_limit.exceeded', 429), words.partBooks(words.rateLoad), [words.retry]],
    ['importObsidianBooks', refuse('importObsidianBooks', 'quota.project_books', 409), /^Книги не загружены: /u, []],
    ['importObsidianBooks', refuse('importObsidianBooks', 'server.error', 500), words.partBooks(words.interrupted), [words.loadBooksAgain]],
  ] as const)('says %s %#', async (operation, error, said, exits) => {
    const { api, saga } = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    vi.spyOn(api, operation).mockRejectedValue(error);
    await previewed(api, saga);
    load();
    const line = await screen.findByText(typeof said === 'string' ? plain(said) : said, undefined, { timeout: 10_000 });
    const block = line.closest('[role="alert"]') as HTMLElement;
    for (const exit of exits) expect(within(block).getByRole('button', { name: exit })).toBeTruthy();
    // The other part did its work.
    expect(await screen.findByText(operation === 'importObsidianBooks' ? /^Создано 2/u : words.bookQueued('Книга 3'), undefined, { timeout: 10_000 })).toBeTruthy();
  });

  it('repeats a write refused as too often by «Повторить» with the same key', async () => {
    const { api, saga } = await chosen({ Места: 'materials:place' });
    const records = vi.spyOn(api, 'importObsidianMaterials').mockRejectedValueOnce(refuse('importObsidianMaterials', 'rate_limit.exceeded', 429));
    await previewed(api, saga);
    load();
    fireEvent.click(within((await screen.findByText(words.partRecords(words.rateLoad), undefined, { timeout: 10_000 })).closest('[role="alert"]') as HTMLElement)
      .getByRole('button', { name: words.retry }));
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(records.mock.calls.map((call) => call[2].idempotencyKey)).toEqual([records.mock.calls[0]![2].idempotencyKey, records.mock.calls[0]![2].idempotencyKey]);
  });

  it('reads the folder of the saga again on `not_linked` of a write', async () => {
    const { api, saga } = await chosen({ Места: 'materials:place' });
    vi.spyOn(api, 'importObsidianMaterials').mockImplementation(async () => {
      await api.unlinkObsidianVault(saga);
      throw new MockApiError(409, 'not linked', 'importObsidianMaterials', 'obsidian.not_linked');
    });
    const view = await previewed(api, saga);
    const reads = vi.spyOn(view.client, 'invalidateQueries');
    load();
    await until(() => expect(reads.mock.calls.some((call) => JSON.stringify(call[0]?.queryKey) === JSON.stringify(obsidianKeys.vault(saga)))).toBe(true));
  });
});
