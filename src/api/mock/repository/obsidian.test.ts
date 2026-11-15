// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';
import type { MockScenarioControl } from '../scenarios';

/** The computer of the app with its local author signed in and a saga of his. */
async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  const saga = await api.createProject({ title: 'Северный предел' });
  return { api, saga: saga.id };
}

const choice = { books: ['Книга 1', 'Книга 2'], materials: [{ path: 'Персонажи', kind: 'character' as const }], exclude: ['Книга 1/Черновики'] };
const everything = {
  books: ['Книга 1', 'Книга 2', 'Книга 3', 'Книга 4'],
  materials: [{ path: 'Персонажи', kind: 'character' as const }, { path: 'Места', kind: 'place' as const }],
};

// specs/2026-11-13-desk-obsidian.md, checklist step 6: the mock answers the operations of the bridge in every state of Data.
describe('the bridge with an Obsidian vault in the mock', () => {
  it('links a saga with the folder the shell picks and unlinks it', async () => {
    const { api, saga } = await theApp();
    expect(await api.getObsidianVault(saga)).toEqual({ linked: false, choosing: false });
    await expect(api.scanObsidianVault(saga)).rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.not_linked' } });
    const { request } = await api.beginObsidianVaultChoice(saga);
    expect(request).toMatch(/^[A-Za-z0-9_-]{43}$/u);
    expect(await api.getObsidianVault(saga)).toEqual({
      linked: true, choosing: false, path: '/home/author/Северный предел', name: 'Северный предел', availability: 'available',
    });
    await api.unlinkObsidianVault(saga);
    expect((await api.getObsidianVault(saga)).linked).toBe(false);
  });

  it('says a choice that waits, a refusal and a window without its shell', async () => {
    const waiting = await theApp({ obsidianShell: 'waits' });
    await waiting.api.beginObsidianVaultChoice(waiting.saga);
    expect(await waiting.api.getObsidianVault(waiting.saga)).toEqual({ linked: false, choosing: true });
    const refused = await theApp({ obsidianShell: { refused: 'not_folder' } });
    await refused.api.beginObsidianVaultChoice(refused.saga);
    expect(await refused.api.getObsidianVault(refused.saga)).toEqual({ linked: false, choosing: false, refused: 'not_folder' });
    const headless = await theApp({ obsidianShell: 'absent' });
    await expect(headless.api.beginObsidianVaultChoice(headless.saga)).rejects.toMatchObject({ status: 503, problem: { code: 'obsidian.choice_unavailable' } });
  });

  it('looks over the example vault and previews what it gives', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const tree = await api.scanObsidianVault(saga);
    expect(tree.folders.map((folder) => [folder.path, folder.hint?.kind ?? null])).toEqual([
      ['.', null], ['Книга 1', 'book'], ['Книга 2', 'skip'], ['Книга 3', 'skip'], ['Книга 4', 'book'], ['Персонажи', 'materials'],
      ['Места', 'materials'], ['Шаблоны', 'skip'],
      // specs/2026-11-15-desk-obsidian-web.md OW7.2: a book with parts and records inside it, one folder deeper.
      ['Книга 5', 'book'], ['Книга 5/Герои', 'materials'], ['Книга 5/Часть 1', 'skip'], ['Книга 5/Часть 2', 'skip'], ['Книга 5/Часть 2/Черновики', null],
    ]);
    const preview = await api.previewObsidianImport(saga, choice);
    expect(preview.books.map((book) => [book.folder, book.title, book.chapters.length, book.warnings.map((warning) => warning.code)])).toEqual([
      ['Книга 1', 'Застава', 3, ['chapters_differ']], ['Книга 2', 'Книга 2', 2, ['book_in_note']],
    ]);
    expect(preview.materials.records.map((record) => [record.name, record.state])).toEqual([
      ['Ольга', 'in_book'], ['Мирон Сватов', 'in_book_by_alias'], ['Йорген', 'new'], ['Йорген', 'other_source'], ['Семья', 'new'],
    ]);
    expect(preview.reading).toEqual({ newChapters: 5, newCharacters: 107, helper: 'none', consent: false });
  });

  it('gives every warning, every reason of a skip and every state of a book and of a record', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const tree = await api.scanObsidianVault(saga);
    const preview = await api.previewObsidianImport(saga, everything);
    expect(new Set(preview.books.flatMap((book) => book.warnings.map((warning) => warning.code))))
      .toEqual(new Set(['chapters_differ', 'book_in_note', 'order_by_name', 'title_cut']));
    expect(new Set(preview.materials.records.flatMap((record) => record.warnings))).toEqual(new Set(['list_note', 'several_sections']));
    const skips = [
      ...tree.skipped, ...tree.folders.flatMap((folder) => folder.skipped), ...preview.books.flatMap((book) => book.skipped), ...preview.materials.skipped,
    ].map((skip) => skip.reason);
    expect(new Set(skips)).toEqual(new Set(['link', 'hidden', 'cloud_only', 'not_note', 'encoding', 'too_big', 'empty', 'service', 'no_name', 'long_name', 'unreadable']));
    expect(preview.books.every((book) => book.state.kind === 'new')).toBe(true);

    // The folders and the notes of the example as part A2 keeps them linked.
    api.setScenario({ ...api.getScenario(), obsidianLinked: true });
    const linkedTree = await api.scanObsidianVault(saga);
    expect(linkedTree.folders.filter((folder) => folder.book).map((folder) => [folder.path, folder.book!.title])).toEqual([
      ['Книга 1', 'Застава'], ['Книга 2', 'Белый порт'],
    ]);
    const linked = await api.previewObsidianImport(saga, everything);
    expect(linked.books.map((book) => [book.folder, book.state.kind, book.state.changed, book.state.inTrash])).toEqual([
      ['Книга 1', 'linked', true, false], ['Книга 2', 'linked', false, true], ['Книга 3', 'new', false, false], ['Книга 4', 'new', false, false],
    ]);
    expect(new Set(linked.materials.records.map((record) => record.state)))
      .toEqual(new Set(['new', 'in_book', 'in_book_by_alias', 'same_note', 'edited_in_app', 'other_source']));
    expect(linked.materials.records.filter((record) => record.state === 'same_note').map((record) => record.changed)).toEqual([false, true]);
    // The book linked and not changed since its load is not read again.
    expect(linked.reading.newChapters).toBe(preview.reading.newChapters - 2);
  });

  it('refuses a choice by every code of Data', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    await expect(api.previewObsidianImport(saga, { books: ['../Книга'] })).rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    await expect(api.previewObsidianImport(saga, { books: ['Нет такой'] })).rejects.toMatchObject({ status: 400 });
    await expect(api.previewObsidianImport(saga, { books: ['Книга 1', 'Книга 1/Часть'] })).rejects.toMatchObject({
      status: 422, problem: { code: 'obsidian.folders_overlap', folders: ['Книга 1', 'Книга 1/Часть'] },
    });
    for (const [vault, status, code, extra] of [
      [{ missing: 'not_found' }, 409, 'obsidian.vault_missing', { vaultReason: 'not_found' }],
      [{ missing: 'not_folder' }, 409, 'obsidian.vault_missing', { vaultReason: 'not_folder' }],
      [{ missing: 'unreadable' }, 409, 'obsidian.vault_missing', { vaultReason: 'unreadable' }],
      ['busy', 429, 'obsidian.busy', {}],
      ['slow', 503, 'obsidian.slow', {}],
      ['too_large', 422, 'obsidian.too_large', { notes: 20_412, limitNotes: 20_000 }],
      ['too_many_entries', 422, 'obsidian.too_large', { entries: 200_001, limitEntries: 200_000 }],
    ] as const) {
      api.setScenario({ ...api.getScenario(), obsidianVault: vault });
      await expect(api.previewObsidianImport(saga, choice)).rejects.toMatchObject({ status, problem: { code, ...extra } });
    }
    api.setScenario({ ...api.getScenario(), obsidianVault: { missing: 'not_folder' } });
    expect((await api.getObsidianVault(saga)).availability).toBe('missing');
    api.setScenario({ ...api.getScenario(), obsidianVault: 'unknown' });
    expect((await api.getObsidianVault(saga)).availability).toBe('unknown');
    api.setScenario({ ...api.getScenario(), obsidianVault: 'truncated' });
    expect(await api.scanObsidianVault(saga)).toMatchObject({ truncated: true, entries: 200_000 });
  });
});

const fingerprintOf = { 'Книга 1': '6f1c2b0e9a8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b', 'Книга 3': '9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e8d' };
const emptyGlue = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';

/** A saga of the app linked with the example vault, with a book of its own. */
async function aLinkedSaga(scenario: Partial<MockScenarioControl> = {}) {
  const app = await theApp(scenario);
  await app.api.beginObsidianVaultChoice(app.saga);
  const book = await app.api.createBook(app.saga, { title: 'Застава' });
  return { ...app, book: book.id };
}

// specs/2026-11-14-desk-obsidian-books.md, checklist step 5: the mock answers the writes of the books of the folders in every
// outcome and every refusal of Data.
describe('the books of the folders of an Obsidian vault in the mock', () => {
  it('loads the folders of books: queued, linked, in the trash, refused; the same key answers the same', async () => {
    const { api, saga, book } = await aLinkedSaga({ obsidianLinked: true });
    await api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 4' });
    const books = [
      { folder: 'Книга 3', fingerprint: fingerprintOf['Книга 3'] }, { folder: 'Книга 4', fingerprint: '3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b' },
      { folder: 'Книга 2', fingerprint: '1d2c3b4a59687766554433221100ffeeddccbbaa99887766554433221100ffee' }, { folder: 'Шаблоны', fingerprint: emptyGlue },
    ];
    const loaded = await api.importObsidianBooks(saga, { books }, { idempotencyKey: 'idem-load-0001' });
    expect(loaded.folders.map((folder) => [folder.folder, folder.outcome, folder.refusal ?? folder.title ?? null])).toEqual([
      ['Книга 3', 'queued', null], ['Книга 4', 'linked', 'Застава'], ['Книга 2', 'in_trash', 'Белый порт'], ['Шаблоны', 'refused', 'empty_import_content'],
    ]);
    expect(loaded.queue).toEqual({ id: loaded.folders[0]!.queueId, status: 'running' });
    expect((await api.previewObsidianImport(saga, { books: ['Книга 3'] })).books[0]!.state).toEqual({
      kind: 'queued', queueId: loaded.queue!.id, changed: false, inTrash: false,
    });
    expect(await api.importObsidianBooks(saga, { books }, { idempotencyKey: 'idem-load-0001' })).toEqual(loaded);
    // The volume of «Книга 3» is in work: a second load gives its queue without a second volume, and no queue of its own.
    expect(await api.importObsidianBooks(saga, { books: [books[0]!] }, { idempotencyKey: 'idem-load-0002' })).toEqual({
      queue: null, folders: [{ folder: 'Книга 3', outcome: 'queued', queueId: loaded.queue!.id }],
    });
  });

  it('refuses a load by every code of Data', async () => {
    const { api, saga } = await aLinkedSaga();
    const books = [{ folder: 'Книга 1', fingerprint: fingerprintOf['Книга 1'] }];
    await expect(api.importObsidianBooks(saga, { books: [{ folder: 'Книга 1', fingerprint: emptyGlue }] }, { idempotencyKey: 'idem-load-0003' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Книга 1'] } });
    await expect(api.importObsidianBooks(saga, { books: [{ folder: '../Книга', fingerprint: emptyGlue }] }, { idempotencyKey: 'idem-load-0004' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    for (const [write, code, extra] of [
      ['queue_busy', 'obsidian.queue_busy', { queue: { id: 'import-queue-1', status: 'running' } }],
      ['load_interrupted', 'obsidian.load_interrupted', {}],
    ] as const) {
      api.setScenario({ ...api.getScenario(), obsidianWrite: write });
      await expect(api.importObsidianBooks(saga, { books }, { idempotencyKey: `idem-load-${write}` })).rejects.toMatchObject({ status: 409, problem: { code, ...extra } });
    }
    api.setScenario({ ...api.getScenario(), obsidianWrite: undefined, obsidianVault: 'slow' });
    await expect(api.importObsidianBooks(saga, { books }, { idempotencyKey: 'idem-load-0005' })).rejects.toMatchObject({ status: 503, problem: { code: 'obsidian.slow' } });
  });

  it('updates a linked book by «Обновить из файла» and refuses an application by every code of Data', async () => {
    const { api, saga, book } = await aLinkedSaga();
    await expect(api.updateBookFromObsidian(saga, book, { mode: 'preview' }, { idempotencyKey: 'idem-update-0001' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.book_not_linked' } });
    await api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 1' });
    const preview = await api.updateBookFromObsidian(saga, book, { mode: 'preview' }, { idempotencyKey: 'idem-update-0002' });
    expect(preview).toMatchObject({ fingerprint: fingerprintOf['Книга 1'], book: { folder: 'Книга 1', state: { kind: 'linked', bookId: book, changed: true } } });
    expect(preview.job).toMatchObject({ kind: 'import', subject: { bookId: book, preview: true } });
    const applied = await api.updateBookFromObsidian(saga, book, { mode: 'apply', expectedFingerprint: preview.fingerprint! }, { idempotencyKey: 'idem-update-0003' });
    expect(applied).not.toHaveProperty('fingerprint');
    expect(applied.job.subject).toMatchObject({ bookId: book });
    await expect(api.updateBookFromObsidian(saga, book, { mode: 'apply', expectedFingerprint: emptyGlue }, { idempotencyKey: 'idem-update-0004' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Книга 1'] } });
    for (const [write, code, extra] of [
      ['notes_unread', 'obsidian.notes_unread', { unread: [{ reason: 'cloud_only', count: 1 }] }],
      ['nothing_to_apply', 'obsidian.nothing_to_apply', {}],
      ['folder_missing', 'obsidian.folder_missing', { folders: ['Книга 1'] }],
      ['queue_busy', 'obsidian.queue_busy', { queue: { id: 'import-queue-1', status: 'running' } }],
    ] as const) {
      api.setScenario({ ...api.getScenario(), obsidianWrite: write });
      await expect(api.updateBookFromObsidian(saga, book, { mode: 'apply', expectedFingerprint: preview.fingerprint! }, { idempotencyKey: `idem-update-${write}` }))
        .rejects.toMatchObject({ status: 409, problem: { code, ...extra } });
    }
    api.setScenario({ ...api.getScenario(), obsidianWrite: 'notes_unread' });
    const unread = await api.updateBookFromObsidian(saga, book, { mode: 'preview' }, { idempotencyKey: 'idem-update-0005' });
    expect(unread.book!.skipped).toContainEqual({ reason: 'cloud_only', count: 1 });
    await api.updateBookFromObsidian(saga, book, { mode: 'apply', expectedFingerprint: preview.fingerprint!, confirmUnread: true }, { idempotencyKey: 'idem-update-0006' });
    await expect(api.updateBookFromObsidian(saga, book, { mode: 'apply' } as never, { idempotencyKey: 'idem-update-0007' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
  });

  it('links a book with a folder without a load, names the linked books and takes the links off', async () => {
    const { api, saga, book } = await aLinkedSaga();
    const other = (await api.createBook(saga, { title: 'Белый порт' })).id;
    await api.linkBookToObsidianFolder(saga, other, { folder: 'Книга 2' });
    await expect(api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 2' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_linked', folders: ['Книга 2'], bookId: other } });
    await expect(api.linkBookToObsidianFolder(saga, book, { folder: '.' }))
      .rejects.toMatchObject({ status: 422, problem: { code: 'obsidian.folders_overlap', folders: ['.', 'Книга 2'] } });
    await expect(api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 9' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_missing', folders: ['Книга 9'] } });
    await expect(api.linkBookToObsidianFolder(saga, 'book-nowhere', { folder: 'Книга 1' })).rejects.toMatchObject({ status: 404 });
    await api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 1' });
    // A new link takes the place of the one before.
    await api.linkBookToObsidianFolder(saga, book, { folder: 'Книга 3' });
    expect((await api.getObsidianVault(saga)).books).toEqual([
      { bookId: other, title: 'Белый порт', folder: 'Книга 2', loadedAt: null, presence: 'present', excluded: [], nested: [] },
      { bookId: book, title: 'Застава', folder: 'Книга 3', loadedAt: null, presence: 'present', excluded: [], nested: [] },
    ]);
    // A deleted, B linked with its folder, A given back: B holds the folder, A is to be linked anew (OK2.6).
    const first = (await api.createBook(saga, { title: 'Берег' })).id;
    const second = (await api.createBook(saga, { title: 'Берег (второй)' })).id;
    await api.linkBookToObsidianFolder(saga, first, { folder: 'Книга 4' });
    await api.deleteBook(first);
    await api.linkBookToObsidianFolder(saga, second, { folder: 'Книга 4' });
    await api.restoreBook(first);
    const anew = await api.getObsidianVault(saga);
    expect(anew.books?.find((one) => one.folder === 'Книга 4')?.bookId).toBe(second);
    expect(anew.relink).toEqual([{ bookId: first, title: 'Берег', folder: 'Книга 4', loadedAt: null, presence: 'present', excluded: [], nested: [] }]);
    api.setScenario({ ...api.getScenario(), obsidianVault: 'unknown' });
    expect((await api.getObsidianVault(saga)).books?.every((one) => one.presence === 'unknown')).toBe(true);
    api.setScenario({ ...api.getScenario(), obsidianVault: undefined, obsidianLinked: true });
    expect((await api.getObsidianVault(saga)).relink).toEqual([
      { bookId: first, title: 'Берег', folder: 'Книга 4', loadedAt: null, presence: 'present', excluded: [], nested: [] },
      { bookId: '5a7b9c1d-2e4f-4a6b-8c0d-1e2f3a4b5c6d', title: 'Застава (прежняя)', folder: 'Книга 1', loadedAt: null, presence: 'present', excluded: [], nested: [] },
    ]);
    api.setScenario({ ...api.getScenario(), obsidianLinked: false });
    await api.unlinkBookFromObsidianFolder(saga, book);
    expect((await api.getObsidianVault(saga)).books?.map((one) => one.bookId)).toEqual([other, second]);
    await api.unlinkObsidianVault(saga);
    await api.beginObsidianVaultChoice(saga);
    expect((await api.getObsidianVault(saga)).books).toBeUndefined();
  });
});

type Preview = Awaited<ReturnType<ReturnType<typeof createMockApiClient>['previewObsidianImport']>>;

/** A load of the records of what a preview of the folders of materials showed, with the decisions. */
function notesOf(preview: Preview, decisions: { replace?: Array<'edited_in_app' | 'other_source'>; decide?: { [path: string]: 'replace' | 'keep' } } = {}) {
  return {
    materials: preview.materials.folders.map((folder) => ({
      path: folder.folder, kind: everything.materials.find((material) => material.path === folder.folder)!.kind, fingerprint: folder.fingerprint,
    })),
    records: preview.materials.records.map((record) => ({
      path: record.path, seen: record.seen, ...(decisions.decide?.[record.path] ? { decision: decisions.decide[record.path]! } : {}),
    })),
    replace: decisions.replace ?? [],
  };
}

const group = (count: number, names: string[]) => ({ count, names });

// specs/2026-11-15-desk-obsidian-notes.md, checklist step 5: the mock answers the load of the records in every group of its
// answer and every refusal of Data.
describe('the records of the notes of an Obsidian vault in the mock', () => {
  it('loads the records by their states, what the author saw and his decisions; the same key answers the same', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { materials: everything.materials });
    expect(preview.materials.folders.map((folder) => folder.folder)).toEqual(['Персонажи', 'Места']);
    // ON1.2: of other_source the preview says whether a decision may replace the record that keeps the name, or which note
    // of the load has it.
    expect(preview.materials.records.filter((record) => record.state === 'other_source').map((record) => [record.path, record.replaceable, record.heldBy]))
      .toEqual([['Персонажи/Йорген старший.md', false, 'Персонажи/Йорген.md'], ['Места/Перевал.md', true, undefined]]);
    const loaded = await api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0001' });
    expect(loaded.created).toEqual(group(6, ['Ольга', 'Мирон Сватов', 'Йорген', 'Семья', 'Все места', 'Застава']));
    expect(loaded.kept.taken).toEqual(group(2, ['Йорген', 'Перевал']));
    expect(loaded.skipped).toEqual([{ reason: 'long_name', count: 1 }, { reason: 'no_name', count: 1 }]);
    expect(await api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0001' })).toEqual(loaded);
    // The mock remembers what it loaded (specs/2026-11-15-desk-obsidian-web.md OW7.2): a new preview finds the same notes.
    const second = await api.previewObsidianImport(saga, { materials: everything.materials });
    expect(second.materials.records.filter((record) => record.state === 'same_note').map((record) => record.name))
      .toEqual(['Ольга', 'Мирон Сватов', 'Йорген', 'Семья', 'Все места', 'Застава']);
    // A name a record of the author keeps is replaced by a decision; a name another note of the load gets never is.
    const replaced = await api.importObsidianMaterials(saga, notesOf(second, { replace: ['other_source'] }), { idempotencyKey: 'idem-notes-0002' });
    expect([replaced.replaced, replaced.kept.taken, replaced.unchanged.count]).toEqual([group(1, ['Перевал']), group(1, ['Йорген']), 6]);
    // What the author did not see is kept as changed since the preview.
    const stale = notesOf(await api.previewObsidianImport(saga, { materials: everything.materials }));
    stale.records[0] = { ...stale.records[0]!, seen: '0000000000000000' };
    expect((await api.importObsidianMaterials(saga, stale, { idempotencyKey: 'idem-notes-0003' })).kept.changed).toEqual(group(1, ['Ольга']));

    api.setScenario({ ...api.getScenario(), obsidianLinked: true });
    const linked = await api.previewObsidianImport(saga, { materials: everything.materials });
    const again = await api.importObsidianMaterials(saga, notesOf(linked), { idempotencyKey: 'idem-notes-0004' });
    expect([again.unchanged, again.updated, again.kept.edited]).toEqual([group(1, ['Семья']), group(1, ['Перевал']), group(1, ['Йорген'])]);
    const decided = await api.importObsidianMaterials(saga, notesOf(linked, { replace: ['edited_in_app'], decide: { 'Места/Перевал.md': 'keep' } }), { idempotencyKey: 'idem-notes-0005' });
    expect(decided.updated).toEqual(group(2, ['Йорген', 'Перевал']));
  });

  it('refuses a load of the records by every code of Data', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { materials: everything.materials });
    const changed = notesOf(preview);
    changed.materials[1] = { ...changed.materials[1]!, fingerprint: emptyGlue };
    await expect(api.importObsidianMaterials(saga, changed, { idempotencyKey: 'idem-notes-0010' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Места'] } });
    await expect(api.importObsidianMaterials(saga, { ...notesOf(preview), records: [{ path: 'Места/Перевал.md', seen: 'ABC' }] }, { idempotencyKey: 'idem-notes-0011' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    // ON1.3: a load says what the author saw of every record of its folders.
    const unseen = notesOf(preview);
    unseen.records = unseen.records.filter((record) => record.path !== 'Места/Перевал.md');
    await expect(api.importObsidianMaterials(saga, unseen, { idempotencyKey: 'idem-notes-0015' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    // The author took a name during the load: conflict, nothing kept — the same key then loads.
    api.setScenario({ ...api.getScenario(), obsidianWrite: 'conflict' });
    await expect(api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0016' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'conflict' } });
    api.setScenario({ ...api.getScenario(), obsidianWrite: undefined });
    expect((await api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0016' })).created.count).toBe(6);
    api.setScenario({ ...api.getScenario(), obsidianVault: 'too_many_records' });
    const tooMany = { status: 422, problem: { code: 'obsidian.too_large', records: 5_003, sagaRecords: 5_003, limitRecords: 5_000 } };
    await expect(api.previewObsidianImport(saga, { materials: everything.materials })).rejects.toMatchObject(tooMany);
    await expect(api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0012' })).rejects.toMatchObject(tooMany);
    api.setScenario({ ...api.getScenario(), obsidianVault: 'slow' });
    await expect(api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0013' })).rejects.toMatchObject({ status: 503, problem: { code: 'obsidian.slow' } });
    api.setScenario({ ...api.getScenario(), obsidianVault: undefined });
    await api.unlinkObsidianVault(saga);
    await expect(api.importObsidianMaterials(saga, notesOf(preview), { idempotencyKey: 'idem-notes-0014' })).rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.not_linked' } });
  });
});

const places = [{ path: 'Места', kind: 'place' as const }];

/** A load of the records of a preview as the page sends it: every folder of materials with its fingerprint, every record seen. */
function loadOf(preview: Preview, choice: { books?: string[]; materials: Array<{ path: string; kind: 'place' | 'character' }>; exclude?: string[] }) {
  return {
    books: choice.books ?? [],
    materials: preview.materials.folders.map((folder) => ({ path: folder.folder, kind: choice.materials.find((one) => one.path === folder.folder)!.kind, fingerprint: folder.fingerprint })),
    exclude: choice.exclude ?? [],
    records: preview.materials.records.map((record) => ({ path: record.path, seen: record.seen })),
  };
}

// specs/2026-11-15-desk-obsidian-web.md OW7.2: what the path of the author needs of the mock — a load of books is a load of
// «Книги», a load of records puts them into «Ваши материалы», an exclusion changes what a folder gives, the example has nested
// folders, and an unlink takes the links off.
// specs/2026-11-15-desk-obsidian-web.md OW7.2 (Sm8 of the review): the mock is not kinder than the server — a body the contract
// does not take, folders that overlap the folders of the books of the saga or are named twice, a folder gone since its preview,
// and a vault read by another before the answer of the key.
describe('the mock of the bridge is not kinder than the server', () => {
  it('refuses a field the contract does not name, at the top of a body and in its items', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const extra = (body: object) => body as never;
    await expect(api.previewObsidianImport(saga, extra({ books: ['Книга 3'], mode: 'all' }))).rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    await expect(api.previewObsidianImport(saga, extra({ materials: [{ path: 'Места', kind: 'place', fingerprint: 'x' }] }))).rejects.toMatchObject({ status: 400 });
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 3'], materials: places });
    const fingerprint = preview.books[0]!.fingerprint;
    await expect(api.importObsidianBooks(saga, extra({ books: [{ folder: 'Книга 3', fingerprint, title: 'Весна' }] }), { idempotencyKey: 'idem-strict-0001' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    await expect(api.importObsidianMaterials(saga, extra({ ...loadOf(preview, { materials: places }), seenAll: true }), { idempotencyKey: 'idem-strict-0002' }))
      .rejects.toMatchObject({ status: 400, problem: { code: 'request.validation' } });
    await expect(api.importObsidianMaterials(saga, extra({ ...loadOf(preview, { materials: places }), records: [{ path: 'Места/Перевал.md', seen: '0123456789abcdef', state: 'new' }] }), { idempotencyKey: 'idem-strict-0003' }))
      .rejects.toMatchObject({ status: 400 });
  });

  it('refuses a folder named twice and a book around or inside a folder of a book of the saga — 422 with both folders', async () => {
    const { api, saga } = await theApp({ obsidianLinked: true });
    await api.beginObsidianVaultChoice(saga);
    await expect(api.previewObsidianImport(saga, { books: ['Места'], materials: [{ path: 'Места', kind: 'place' }] }))
      .rejects.toMatchObject({ status: 422, problem: { code: 'obsidian.folders_overlap', folders: ['Места', 'Места'] } });
    await expect(api.previewObsidianImport(saga, { books: ['.'] }))
      .rejects.toMatchObject({ status: 422, problem: { code: 'obsidian.folders_overlap', folders: ['.', 'Книга 1'] } });
    // The folder of a book of the saga itself is no overlap: it is that book.
    expect((await api.previewObsidianImport(saga, { books: ['Книга 1'] })).books[0]!.state.kind).toBe('linked');
    await expect(api.importObsidianBooks(saga, { books: [{ folder: '.', fingerprint: 'a'.repeat(64) }] }, { idempotencyKey: 'idem-strict-0004' }))
      .rejects.toMatchObject({ status: 422, problem: { code: 'obsidian.folders_overlap', folders: ['.', 'Книга 1'] } });
  });

  it('says a folder gone since its preview by folder_changed — of the books and of the records', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    await expect(api.importObsidianBooks(saga, { books: [{ folder: 'Пропала', fingerprint: 'a'.repeat(64) }] }, { idempotencyKey: 'idem-strict-0005' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Пропала'] } });
    const preview = await api.previewObsidianImport(saga, { materials: places });
    const body = loadOf(preview, { materials: places });
    await expect(api.importObsidianMaterials(saga, { ...body, materials: [...body.materials, { path: 'Пропала', kind: 'place', fingerprint: 'b'.repeat(64) }] }, { idempotencyKey: 'idem-strict-0006' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Пропала'] } });
  });

  it('says a vault read by another before the answer of the key, as the server: its place first', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 3'], materials: places });
    const books = { books: [{ folder: 'Книга 3', fingerprint: preview.books[0]!.fingerprint }] };
    await api.importObsidianBooks(saga, books, { idempotencyKey: 'idem-strict-0007' });
    await api.importObsidianMaterials(saga, loadOf(preview, { materials: places }), { idempotencyKey: 'idem-strict-0008' });
    api.setScenario({ ...api.getScenario(), obsidianVault: 'busy' });
    await expect(api.importObsidianBooks(saga, books, { idempotencyKey: 'idem-strict-0007' })).rejects.toMatchObject({ status: 429, problem: { code: 'obsidian.busy' } });
    await expect(api.importObsidianMaterials(saga, loadOf(preview, { materials: places }), { idempotencyKey: 'idem-strict-0008' }))
      .rejects.toMatchObject({ status: 429, problem: { code: 'obsidian.busy' } });
  });
});

describe('what the mock remembers of a load of an Obsidian vault', () => {
  it('loads the books as volumes of «Книги» that give books, and the folder is linked with its book', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 3'] });
    const loaded = await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 3', fingerprint: preview.books[0]!.fingerprint }] }, { idempotencyKey: 'idem-books-0101' });
    const queue = (await api.listImportQueues(saga)).data[0]!;
    expect(queue).toMatchObject({ id: loaded.queue!.id, status: 'running' });
    expect(queue.items.map((item) => [item.sourceFileName, item.preflightStatus])).toEqual([['Книга 3.md', 'ready']]);
    expect((await api.previewObsidianImport(saga, { books: ['Книга 3'] })).books[0]!.state.kind).toBe('queued');
    await api.getJob(queue.items[0]!.importJobId!);
    const bookId = (await api.getImportQueue(saga, queue.id)).items[0]!.bookId!;
    expect((await api.getBook(bookId)).title).toBe('Книга 3');
    expect((await api.getObsidianVault(saga)).books).toEqual([{ bookId, title: 'Книга 3', folder: 'Книга 3', loadedAt: null, presence: 'present', excluded: [], nested: [] }]);
    expect((await api.previewObsidianImport(saga, { books: ['Книга 3'] })).books[0]!.state).toMatchObject({ kind: 'linked', bookId, inTrash: false });
    expect((await api.scanObsidianVault(saga)).folders.find((folder) => folder.path === 'Книга 3')!.book).toEqual({ bookId, title: 'Книга 3' });
  });

  it('keeps the composition of a load with the link of its book: changes — only by a glue of another composition (OW2.5)', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const choice = { books: ['Книга 5'], materials: [{ path: 'Книга 5/Герои', kind: 'character' as const }], exclude: ['Книга 5/Часть 2/Черновики'] };
    const preview = await api.previewObsidianImport(saga, choice);
    await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 5', fingerprint: preview.books[0]!.fingerprint }], materials: choice.materials, exclude: choice.exclude }, { idempotencyKey: 'idem-books-0105' });
    const queue = (await api.listImportQueues(saga)).data[0]!;
    await api.getJob(queue.items[0]!.importJobId!);
    const [book] = (await api.getObsidianVault(saga)).books!;
    expect(book).toMatchObject({ folder: 'Книга 5', excluded: ['Книга 5/Часть 2/Черновики'], nested: ['Книга 5/Герои'] });
    // The same composition — no change; the folders of records without a kind and the whole folder — a change.
    expect((await api.previewObsidianImport(saga, { ...choice, materials: [{ path: 'Книга 5/Герои' }] })).books[0]!.state).toMatchObject({ kind: 'linked', changed: false });
    expect((await api.previewObsidianImport(saga, { books: ['Книга 5'] })).books[0]!.state).toMatchObject({ kind: 'linked', changed: true });
  });

  it('puts the records into «Ваши материалы» with their notes, and a new preview finds them; an edit and a deletion are seen', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { materials: places });
    await api.importObsidianMaterials(saga, loadOf(preview, { materials: places }), { idempotencyKey: 'idem-notes-0101' });
    const records = (await api.listAuthorMaterials(saga)).data;
    expect(records.map((record) => [record.name, record.kind, record.sourceKind, record.sourceName])).toEqual([
      ['Все места', 'place', 'obsidian', 'Места/Все места.md'], ['Застава', 'place', 'obsidian', 'Места/Застава.md'],
    ]);
    const again = await api.previewObsidianImport(saga, { materials: places });
    expect(again.materials.records.map((record) => [record.name, record.state])).toEqual([['Перевал', 'other_source'], ['Все места', 'same_note'], ['Застава', 'same_note']]);
    const outpost = records.find((record) => record.name === 'Застава')!;
    await api.updateAuthorMaterial(saga, outpost.id, { name: 'Застава', kind: 'place', description: 'Правка в приложении', properties: [], expectedRevision: outpost.revision });
    expect((await api.previewObsidianImport(saga, { materials: places })).materials.records.find((record) => record.name === 'Застава')!.state).toBe('edited_in_app');
    const edited = (await api.listAuthorMaterials(saga)).data.find((record) => record.name === 'Застава')!;
    await api.deleteAuthorMaterial(saga, edited.id, edited.revision);
    expect((await api.previewObsidianImport(saga, { materials: places })).materials.records.find((record) => record.name === 'Застава')!.state).toBe('new');
  });

  it('gives a book with parts and records inside it by the choice, and an exclusion changes what and the fingerprints', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const together = await api.previewObsidianImport(saga, { books: ['Книга 5'] });
    expect(together.books[0]!.chapters.map((chapter) => chapter.title)).toEqual(['Встреча', 'Дорога', 'Мост', 'Зимовье', 'Набросок', 'Тим', 'Сава']);
    const heroes = [{ path: 'Книга 5/Герои', kind: 'character' as const }];
    const whole = await api.previewObsidianImport(saga, { books: ['Книга 5'], materials: heroes });
    expect(whole.books[0]!.chapters.map((chapter) => chapter.title)).toEqual(['Встреча', 'Дорога', 'Мост', 'Зимовье', 'Набросок']);
    expect(whole.materials.records.map((record) => [record.name, record.folder])).toEqual([['Тим', 'Книга 5/Герои'], ['Сава', 'Книга 5/Герои']]);
    const exclude = ['Книга 5/Часть 2/Черновики', 'Книга 5/Герои/Сава.md'];
    const narrower = await api.previewObsidianImport(saga, { books: ['Книга 5'], materials: heroes, exclude });
    expect(narrower.books[0]!.chapters.map((chapter) => chapter.title)).toEqual(['Встреча', 'Дорога', 'Мост', 'Зимовье']);
    expect(narrower.books[0]!.fingerprint).not.toBe(whole.books[0]!.fingerprint);
    expect(narrower.materials.records.map((record) => record.name)).toEqual(['Тим']);
    expect(narrower.materials.folders[0]!.fingerprint).not.toBe(whole.materials.folders[0]!.fingerprint);
    // A load of the choice with the exclusions and the fingerprints of the preview without them: the folders changed.
    await expect(api.importObsidianBooks(saga, { books: [{ folder: 'Книга 5', fingerprint: whole.books[0]!.fingerprint }], materials: heroes, exclude }, { idempotencyKey: 'idem-books-0102' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Книга 5'] } });
    await expect(api.importObsidianMaterials(saga, { ...loadOf(whole, { books: ['Книга 5'], materials: heroes }), exclude }, { idempotencyKey: 'idem-notes-0102' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'obsidian.folder_changed', folders: ['Книга 5/Герои'] } });
  });

  it('after an unlink meets the records as of another source to replace, and the folders of books as new', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 3'], materials: places });
    await api.importObsidianMaterials(saga, loadOf(preview, { books: ['Книга 3'], materials: places }), { idempotencyKey: 'idem-notes-0103' });
    const loaded = await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 3', fingerprint: preview.books[0]!.fingerprint }], materials: places }, { idempotencyKey: 'idem-books-0103' });
    await api.getJob((await api.getImportQueue(saga, loaded.queue!.id)).items[0]!.importJobId!);
    expect((await api.previewObsidianImport(saga, { books: ['Книга 3'] })).books[0]!.state.kind).toBe('linked');
    await api.unlinkObsidianVault(saga);
    await api.beginObsidianVaultChoice(saga);
    const after = await api.previewObsidianImport(saga, { books: ['Книга 3'], materials: places });
    expect(after.books[0]!.state.kind).toBe('new');
    expect(after.materials.records.filter((record) => record.name !== 'Перевал').map((record) => [record.name, record.state, record.replaceable, record.heldBy ?? null]))
      .toEqual([['Все места', 'other_source', true, null], ['Застава', 'other_source', true, null]]);
    const replaced = await api.importObsidianMaterials(saga, { ...loadOf(after, { books: ['Книга 3'], materials: places }), replace: ['other_source'] }, { idempotencyKey: 'idem-notes-0104' });
    expect(replaced.replaced.names).toEqual(['Перевал', 'Все места', 'Застава']);
    expect((await api.previewObsidianImport(saga, { materials: places })).materials.records.map((record) => record.state)).toEqual(['same_note', 'same_note', 'same_note']);
  });

  // specs/2026-11-15-desk-obsidian-book-web.md, the mock: the update of a book from its folder by the operations of «Обновить из
  // файла» of the mock — the plan, the update, «Вернуть» —, the notes edited after the load by the scenario.
  it('updates a book loaded from its folder once its notes are edited: its glue, the plan, the update, then no changes', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 1'] });
    const loaded = await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 1', fingerprint: preview.books[0]!.fingerprint }] }, { idempotencyKey: 'idem-edit-0001' });
    await api.getJob((await api.getImportQueue(saga, loaded.queue!.id)).items[0]!.importJobId!);
    const [book] = (await api.getObsidianVault(saga)).books!;
    // The notes as they were loaded: no change, in the preview of the bridge and of the update.
    expect((await api.previewObsidianImport(saga, { books: ['Книга 1'] })).books[0]!.state).toMatchObject({ kind: 'linked', changed: false });
    const same = await api.updateBookFromObsidian(saga, book!.bookId, { mode: 'preview' }, { idempotencyKey: 'idem-edit-0002' });
    expect(same.book!.state.changed).toBe(false);

    // Edited in Obsidian: one chapter more, another glue.
    api.setScenario({ ...api.getScenario(), obsidianEdited: true });
    const bridge = (await api.previewObsidianImport(saga, { books: ['Книга 1'] })).books[0]!;
    expect(bridge.state).toMatchObject({ kind: 'linked', changed: true });
    expect(bridge.chapters.map((chapter) => chapter.title)).toEqual(['Пролог', 'Переправа', 'Интерлюдия', 'Ночной дозор']);
    const edited = await api.updateBookFromObsidian(saga, book!.bookId, { mode: 'preview' }, { idempotencyKey: 'idem-edit-0003' });
    expect(edited.book!.state.changed).toBe(true);
    expect(edited.fingerprint).toBe(bridge.fingerprint);
    expect(edited.fingerprint).not.toBe(same.fingerprint);
    expect((await api.getJob(edited.job.id)).result).toMatchObject({ type: 'import', importMode: 'update_preview' });
    const applied = await api.updateBookFromObsidian(saga, book!.bookId, { mode: 'apply', expectedFingerprint: edited.fingerprint! }, { idempotencyKey: 'idem-edit-0004' });
    await api.getJob(applied.job.id);
    expect((await api.getBook(book!.bookId)).update).toMatchObject({ jobId: applied.job.id });
    expect((await api.previewBookUpdateRevert(book!.bookId)).bookId).toBe(book!.bookId);
    // The book has the glue of the folder now.
    expect((await api.updateBookFromObsidian(saga, book!.bookId, { mode: 'preview' }, { idempotencyKey: 'idem-edit-0005' })).book!.state.changed).toBe(false);
    expect((await api.previewObsidianImport(saga, { books: ['Книга 1'] })).books[0]!.state).toMatchObject({ kind: 'linked', changed: false });
  });
});
