// SPDX-License-Identifier: AGPL-3.0-only
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock/scenarios';
import { manuscriptQueryKeys, type ProjectJob } from '../../../entities/manuscript/api';
import { NetworkApiError, type OperationRequest } from '../../../shared/api';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { aBookOfItsFolder, axeViolations, menuOf, renderInTheApp, toastsText, type TheApp } from './obsidian.test-helpers';
import { updateWords, words } from './words';

vi.mock('@edition', async () => await import('..'));

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  vi.stubGlobal('matchMedia', (query: string) => ({ addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined }));
});
afterEach(() => {
  cleanup();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

type Body = OperationRequest<'updateBookFromObsidian'>;

/** «Книги» of the app with the book of the folder «Книга 1» of the example; the calls of «Обновить из Obsidian» recorded. */
async function theShelf(scenario: Parameters<typeof aBookOfItsFolder>[0] = { obsidianEdited: true }, before: (api: TheApp) => void = () => undefined) {
  const app = await aBookOfItsFolder();
  before(app.api);
  // The notes are edited after the load (the scenario of the example), unless the test says otherwise.
  app.api.setScenario({ ...app.api.getScenario(), ...scenario });
  // The operation of the mock itself, which the spy of a test may call through.
  const real = app.api.updateBookFromObsidian.bind(app.api);
  const update = vi.spyOn(app.api, 'updateBookFromObsidian');
  const view = renderInTheApp(app.api, `/manuscript/books?projectId=${app.saga}`);
  await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
  return { ...app, real, update, view };
}

/** «Обновить из Obsidian…» of the menu of the book: the window. */
async function openWindow() {
  const item = await waitFor(() => within(menuOf('Застава')).getByRole('menuitem', { name: updateWords.updateItem }), { timeout: 10_000 });
  fireEvent.click(item);
  return screen.findByRole('dialog', { name: updateWords.title });
}

const keysOf = (update: { mock: { calls: unknown[][] } }): Array<[Body['mode'], string]> => update.mock.calls.map((call) => [
  (call[2] as Body).mode, (call[3] as { idempotencyKey: string }).idempotencyKey,
]);

type Shelf = Awaited<ReturnType<typeof theShelf>>;

/** Holds the answer of the server to a call until the test lets it go. */
function holdAnswer({ real, update }: Pick<Shelf, 'real' | 'update'>, mode: Body['mode']) {
  let release: () => void = () => undefined;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  update.mockImplementation(async (...args) => {
    if (args[2].mode === mode) await gate;
    return real(...args);
  });
  return { release: () => release(), spy: update };
}

/** Holds the preview Job of the update going while the test says so. */
function holdPreviewJob(api: TheApp) {
  const getJob = api.getJob.bind(api);
  let going = true;
  vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
    const job = await getJob(id);
    return going && job.subject.type === 'import' && job.subject.preview ? { ...job, status: 'running', canCancel: true, result: null } as ProjectJob : job;
  });
  return () => { going = false; };
}

function problem(status: number, code: string, fields: Record<string, unknown> = {}) {
  return new MockApiError(status, code, 'updateBookFromObsidian', code, { fields: fields as never });
}

// specs/2026-11-15-desk-obsidian-book-web.md OU2.1, OU2.2: the owner of «Новая версия из файла…» reads the folder in two phases
// and the keys of its writes are new where the author asks anew and the same where the connection is lost.
describe('the window «Обновить книгу из Obsidian»: the reading and its keys', () => {
  it('reads in two phases under one line; «Отмена» before the answer closes the window and stops the Job that comes after', { timeout: 40_000 }, async () => {
    const shelf = await theShelf();
    const { api } = shelf;
    const held = holdAnswer(shelf, 'preview');
    const cancel = vi.spyOn(api, 'cancelJob');
    const window = await openWindow();
    expect(within(window).getByRole('status').textContent).toContain(updateWords.reading);
    expect(window.textContent).toContain('«Застава» не меняется, пока вы не нажмёте «Обновить книгу».');
    expect(window.textContent).toContain(updateWords.folder('Книга 1'));
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());
    held.release();
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1), { timeout: 10_000 });
    const late = (await held.spy.mock.results[0]!.value) as { job: ProjectJob };
    expect(cancel).toHaveBeenCalledWith(late.job.id);
  });

  it('stops the Job of the preview that comes after the author left the page in the first phase', { timeout: 40_000 }, async () => {
    const shelf = await theShelf();
    const { api, saga, view } = shelf;
    const held = holdAnswer(shelf, 'preview');
    const cancel = vi.spyOn(api, 'cancelJob');
    await openWindow();
    act(() => view.go(`/memory?projectId=${saga}`));
    // The page of «Что помнит помощник» stands in the place of «Книги»: the shelf, its window and their owner are gone.
    await screen.findByRole('heading', { name: /Что помнит помощник/u }, { timeout: 15_000 });
    expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull();
    expect(screen.queryByRole('article', { name: 'Застава' })).toBeNull();
    held.release();
    const late = (await held.spy.mock.results[0]!.value) as { job: ProjectJob };
    await waitFor(() => expect(cancel).toHaveBeenCalledWith(late.job.id), { timeout: 10_000 });
  });

  it('stops the Job of the preview by «Отмена» pressed after its answer came and before the window was drawn again', { timeout: 40_000 }, async () => {
    const shelf = await theShelf();
    const { api, view } = shelf;
    const held = holdAnswer(shelf, 'preview');
    const cancel = vi.spyOn(api, 'cancelJob');
    const window = await openWindow();
    const press = within(window).getByRole('button', { name: words.cancel });
    held.release();
    const answer = (await held.spy.mock.results[0]!.value) as { job: ProjectJob };
    // The answer is taken (its Job is in the cache) before React draws the window again: only microtasks went by.
    while (!view.client.getQueryData(manuscriptQueryKeys.job(answer.job.id))) await Promise.resolve();
    fireEvent.click(press);
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());
    await waitFor(() => expect(cancel).toHaveBeenCalledWith(answer.job.id), { timeout: 10_000 });
  });

  it('stops the Job of the preview by «Отмена» in its second phase', { timeout: 40_000 }, async () => {
    const { api } = await theShelf();
    holdPreviewJob(api);
    const cancel = vi.spyOn(api, 'cancelJob');
    const window = await openWindow();
    await waitFor(() => expect(api.getJob).toHaveBeenCalled());
    expect(window.textContent).toContain(updateWords.reading);
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
  });

  it('reads under a new key at every opening and «Прочитать заново», updates under a new key of every preview, repeats a lost connection under the same', { timeout: 60_000 }, async () => {
    const { real, update } = await theShelf({ obsidianEdited: true, obsidianWrite: 'notes_unread' });
    let window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.readAgain }));
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    const previews = keysOf(update).filter(([mode]) => mode === 'preview').map(([, key]) => key);
    expect(previews).toHaveLength(3);
    expect(new Set(previews).size).toBe(3);

    // The update of this preview: a lost connection, then the same key.
    let lost = true;
    update.mockImplementation(async (...args) => {
      if (args[2].mode === 'apply' && lost) {
        lost = false;
        throw new NetworkApiError(new TypeError('Failed to fetch'));
      }
      return real(...args);
    });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.without }));
    fireEvent.click(await within(window).findByRole('button', { name: words.retry }, { timeout: 10_000 }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });
    const applies = update.mock.calls.filter((call) => (call[2] as Body).mode === 'apply');
    expect(applies).toHaveLength(2);
    expect((applies[0]![3] as { idempotencyKey: string }).idempotencyKey).toBe((applies[1]![3] as { idempotencyKey: string }).idempotencyKey);
    expect(applies.every((call) => (call[2] as Body).confirmUnread === true)).toBe(true);
    expect(previews).not.toContain((applies[0]![3] as { idempotencyKey: string }).idempotencyKey);
  });

  it('repeats a preview whose connection was lost under its key', { timeout: 40_000 }, async () => {
    const { real, update } = await theShelf();
    let lost = true;
    update.mockImplementation(async (...args) => {
      if (lost) {
        lost = false;
        throw new NetworkApiError(new TypeError('Failed to fetch'));
      }
      return real(...args);
    });
    const window = await openWindow();
    fireEvent.click(await within(window).findByRole('button', { name: words.retry }, { timeout: 10_000 }));
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    const [first, second] = keysOf(update);
    expect(first![1]).toBe(second![1]);
  });
});

// OU2.3–OU2.6: the head of the folder, the folders that are not in their place, the plan in the words of the notes, the report.
describe('the window «Обновить книгу из Obsidian»: what it says', () => {
  it('heads with the folder of the book, «Другая папка…» and «Снять связь»; says the plan in the words of the notes with the report', { timeout: 40_000 }, async () => {
    const { api } = await theShelf();
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      if (job.result?.type !== 'import' || !job.result.preview) return job;
      return { ...job, result: { ...job.result, preview: { ...job.result.preview, chapters: job.result.preview.chapters.map((chapter) => ({ ...chapter, editedOnSite: true })) } } };
    });
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    // «Книга 1» of the example: two notes and, edited, four chapters.
    expect(window.textContent).toContain(updateWords.folderCounted('Книга 1', 2, 4));
    expect(within(window).getByRole('button', { name: updateWords.otherFolder })).toBeTruthy();
    expect(within(window).getByRole('button', { name: words.unlinkBook })).toBeTruthy();
    expect(window.textContent).toContain(updateWords.notesKept);
    expect(window.textContent).toContain(updateWords.wrong);
    expect(window.textContent).toContain('Правки, сделанные в приложении в этих главах, заменятся текстом заметок: «');
    expect(window.textContent).toContain(', есть правки в приложении');
    expect(window.textContent).not.toMatch(/файл|сайт|Помощник перечитает|Перечитывать главы/u);
    const report = within(window).getByText(updateWords.report).closest('details')!;
    expect(report.textContent).toContain('Глав получилось');
    expect(report.textContent).toContain('Не вошло в текст:');
    expect(report.textContent).toContain('Пропущено при чтении: 4 заметки');
    expect(within(window).getByRole('button', { name: updateWords.confirm })).toBeTruthy();
    expect(await axeViolations(window)).toEqual([]);
  });

  it('closes «Другая папка…» and «Снять связь» with the reason while a request of the window goes: no window of the link over it', { timeout: 60_000 }, async () => {
    const shelf = await theShelf();
    const { api, real, update } = shelf;
    const unlink = vi.spyOn(api, 'unlinkBookFromObsidianFolder');
    const tools = (window: HTMLElement) => [updateWords.otherFolder, words.unlinkBook].map((name) => {
      const button = within(window).getByRole('button', { name });
      return [button.getAttribute('aria-disabled'), button.getAttribute('aria-description')];
    });
    const closed = [['true', updateWords.reading], ['true', updateWords.reading]];
    const press = (window: HTMLElement) => {
      fireEvent.click(within(window).getByRole('button', { name: updateWords.otherFolder }));
      fireEvent.click(within(window).getByRole('button', { name: words.unlinkBook }));
    };
    // The first phase: the start of the preview.
    const held = holdAnswer(shelf, 'preview');
    const letTheJobGo = holdPreviewJob(api);
    const window = await openWindow();
    expect(tools(window)).toEqual(closed);
    press(window);
    expect(screen.queryByRole('dialog', { name: words.linkTitle })).toBeNull();
    // The second phase: the Job of the preview.
    held.release();
    const answer = (await held.spy.mock.results[0]!.value) as { job: ProjectJob };
    await waitFor(() => expect(api.getJob).toHaveBeenCalledWith(answer.job.id));
    expect(tools(window)).toEqual(closed);
    press(window);
    letTheJobGo();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    expect(tools(window)).toEqual([[null, null], [null, null]]);
    // «Обновить книгу»: the server reads the folder before its answer.
    const apply = holdAnswer({ real, update }, 'apply');
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    await waitFor(() => expect(within(window).getByRole('button', { name: updateWords.confirm }).getAttribute('aria-busy')).toBe('true'));
    expect(tools(window)).toEqual(closed);
    press(window);
    expect(screen.queryByRole('dialog', { name: words.linkTitle })).toBeNull();
    apply.release();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });
    expect(screen.queryByRole('dialog', { name: words.linkTitle })).toBeNull();
    expect(unlink).not.toHaveBeenCalled();
  });

  it('says a plan without a list of chapters and a folder without changes', { timeout: 40_000 }, async () => {
    const { api } = await theShelf();
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      if (job.result?.type !== 'import' || !job.result.preview) return job;
      return { ...job, result: { ...job.result, changedChapters: 0, preview: { ...job.result.preview, chapters: [], moreChapters: 0 } } };
    });
    let window = await openWindow();
    expect(await within(window).findByText(updateWords.noList, undefined, { timeout: 15_000 })).toBeTruthy();
    expect(window.textContent).toContain(updateWords.wrong);
    expect(within(window).getByRole('button', { name: updateWords.confirm })).toBeTruthy();
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());

    // The notes as they were at the load: nothing changes.
    api.setScenario({ ...api.getScenario(), obsidianEdited: false });
    window = await openWindow();
    expect(await within(window).findByRole('heading', { name: updateWords.nothingTitle }, { timeout: 15_000 })).toBeTruthy();
    expect(window.textContent).toContain(updateWords.nothingText('Застава'));
    expect(within(window).queryByRole('button', { name: updateWords.confirm })).toBeNull();
    expect(within(window).getAllByRole('button', { name: updateWords.close }).length).toBeGreaterThan(0);
  });

  it('opens without a reading for a folder of the book that is not in its place, and for a folder of the saga that is not found', { timeout: 40_000 }, async () => {
    let vault: TheApp['getObsidianVault'] = async () => { throw new Error('not read yet'); };
    let missing: 'book' | 'saga' = 'book';
    const { api, update, saga } = await theShelf({ obsidianEdited: true }, (app) => {
      vault = app.getObsidianVault.bind(app);
      vi.spyOn(app, 'getObsidianVault').mockImplementation(async (projectId) => {
        const answer = await vault(projectId);
        return missing === 'book'
          ? { ...answer, books: answer.books?.map((book) => ({ ...book, presence: 'missing' as const })) }
          : { ...answer, availability: 'missing' as const };
      });
    });
    let window = await openWindow();
    expect(window.textContent).toContain(updateWords.folderGone('Книга 1'));
    expect(within(window).getByRole('button', { name: updateWords.otherFolder })).toBeTruthy();
    expect(within(window).getByRole('button', { name: words.unlinkBook })).toBeTruthy();
    expect(update).not.toHaveBeenCalled();
    fireEvent.click(within(window).getAllByRole('button', { name: updateWords.close }).at(-1)!);
    cleanup();

    missing = 'saga';
    const view = renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    window = await openWindow();
    expect(window.textContent).toContain(updateWords.vaultGone);
    expect(update).not.toHaveBeenCalled();
    fireEvent.click(within(window).getByRole('button', { name: updateWords.openBridge }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());
    expect(await within(view.container).findByRole('heading', { level: 1, name: words.pageTitle }, { timeout: 10_000 })).toBeTruthy();
  });

  it('takes the link off by «Снять связь» and opens the window of the link by «Другая папка…»', { timeout: 40_000 }, async () => {
    const { api, saga } = await theShelf();
    const unlink = vi.spyOn(api, 'unlinkBookFromObsidianFolder');
    let window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.otherFolder }));
    const link = await screen.findByRole('dialog', { name: words.linkTitle });
    expect(await within(link).findByRole('radio', { name: /Книга 3/u }, { timeout: 15_000 })).toBeTruthy();
    expect(await axeViolations(link)).toEqual([]);
    fireEvent.click(within(link).getByRole('button', { name: words.cancel }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: words.linkTitle })).toBeNull());

    window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: words.unlinkBook }));
    await waitFor(() => expect(unlink).toHaveBeenCalledWith(saga, expect.any(String)));
    await waitFor(() => expect(toastsText()).toContain(words.unlinkedBook('Застава')));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());
  });

  it('puts «Прочитать заново» first over notes that do not read; «Обновить без этих заметок» sends the word of the author', { timeout: 40_000 }, async () => {
    const { update } = await theShelf({ obsidianEdited: true, obsidianWrite: 'notes_unread' });
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    expect(window.textContent).toContain(updateWords.unread(1, 0));
    expect(window.textContent).toContain('Сейчас не читается 1 заметка (только в облаке — 1). Если обновить без неё, её главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    const again = within(window).getByRole('button', { name: updateWords.readAgain });
    expect(again.getAttribute('data-variant')).toBe('primary');
    expect(within(window).queryByRole('button', { name: updateWords.confirm })).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: updateWords.without }));
    await waitFor(() => expect(update.mock.calls.some((call) => (call[2] as Body).mode === 'apply' && (call[2] as Body).confirmUnread === true)).toBe(true));
  });

  it('says notes that did not read at the update by the numbers of the refusal, and updates without them under the key of the preview', { timeout: 40_000 }, async () => {
    const { real, update } = await theShelf();
    update.mockImplementation(async (...args) => {
      if (args[2].mode === 'apply' && !args[2].confirmUnread) throw problem(409, 'obsidian.notes_unread', { unread: [{ reason: 'cloud_only', count: 2 }, { reason: 'unreadable', count: 1 }] });
      return real(...args);
    });
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    expect(await within(window).findByText(updateWords.unread(2, 1), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(updateWords.unread(2, 1)).toBe('Сейчас не читаются 3 заметки (только в облаке — 2, не открылись — 1). Если обновить без них, их главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    fireEvent.click(within(window).getByRole('button', { name: updateWords.without }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });
    const applies = update.mock.calls.filter((call) => (call[2] as Body).mode === 'apply').map((call) => call[3] as { idempotencyKey: string });
    expect(applies).toHaveLength(2);
    expect(applies[0]!.idempotencyKey).toBe(applies[1]!.idempotencyKey);
  });

  it('updates under a new key after each preview: the update of the preview read again does not repeat the update of the former one', { timeout: 60_000 }, async () => {
    const { real, update } = await theShelf();
    // The update of the first preview: the notes changed while the author looked.
    let first = true;
    update.mockImplementation(async (...args) => {
      if (args[2].mode === 'apply' && first) {
        first = false;
        throw problem(409, 'obsidian.folder_changed');
      }
      return real(...args);
    });
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    expect(await within(window).findByText(updateWords.changedSince, undefined, { timeout: 10_000 })).toBeTruthy();
    fireEvent.click(within(window).getByRole('button', { name: updateWords.readAgain }));
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });

    const calls = keysOf(update);
    expect(calls.map(([mode]) => mode)).toEqual(['preview', 'apply', 'preview', 'apply']);
    const [firstPreview, firstApply, secondPreview, secondApply] = calls.map(([, key]) => key);
    expect(secondPreview).not.toBe(firstPreview);
    expect(secondApply).not.toBe(firstApply);
    expect([firstPreview, secondPreview]).not.toContain(secondApply);
  });
});

// OU2.7: every refusal in its words and with its way out.
describe('the window «Обновить книгу из Obsidian»: the refusals', () => {
  it.each([
    ['obsidian.book_not_linked', 409, updateWords.notLinked, [updateWords.linkItem]],
    ['obsidian.not_linked', 409, updateWords.notLinked, [updateWords.openBridge]],
    ['obsidian.queue_busy', 409, updateWords.queueBusy, []],
    ['obsidian.slow', 503, words.slow, [words.retry]],
    ['obsidian.vault_missing', 409, updateWords.vaultGone, [updateWords.openBridge]],
    ['rate_limit.exceeded', 429, words.rate, [words.retry]],
    ['import.too_large', 422, updateWords.broken('в заметках больше 10 миллионов знаков — разделите папку на несколько книг'), []],
    ['empty_import_content', 422, updateWords.broken('в заметках папки не нашлось текста глав'), []],
  ] as const)('says %s of the reading', { timeout: 40_000 }, async (code, status, said, exits) => {
    const { update } = await theShelf();
    update.mockRejectedValue(problem(status, code));
    const window = await openWindow();
    expect(await within(window).findByText(said, undefined, { timeout: 10_000 })).toBeTruthy();
    for (const exit of exits) expect(within(window).getByRole('button', { name: exit })).toBeTruthy();
    // The line of the folder of the book is not said where the book is no longer linked.
    if (code === 'obsidian.book_not_linked' || code === 'obsidian.not_linked' || code === 'obsidian.vault_missing') {
      expect(window.textContent).not.toContain(updateWords.folder('Книга 1'));
    }
    expect(window.textContent).not.toMatch(/файл|сайт/u);
    // A refusal that waits for the load of «Книги» is not asked again by itself.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(update).toHaveBeenCalledTimes(1);
  });

  it('says the folder of the book that is gone, with «Другая папка…» and «Снять связь»', { timeout: 40_000 }, async () => {
    const { update } = await theShelf();
    update.mockRejectedValue(problem(409, 'obsidian.folder_missing', { folders: ['Книга 1'] }));
    const window = await openWindow();
    expect(await within(window).findByText(updateWords.folderGone('Книга 1'), undefined, { timeout: 10_000 })).toBeTruthy();
    expect(within(window).getByRole('button', { name: updateWords.otherFolder })).toBeTruthy();
    expect(within(window).getByRole('button', { name: words.unlinkBook })).toBeTruthy();
  });

  it.each([
    ['obsidian.folder_changed', updateWords.changedSince, [updateWords.readAgain]],
    ['obsidian.nothing_to_apply', updateWords.nothingToApply, []],
  ] as const)('says %s of the update', { timeout: 40_000 }, async (code, said, exits) => {
    const { real, update } = await theShelf();
    update.mockImplementation(async (...args) => {
      if (args[2].mode === 'apply') throw problem(409, code, { folders: ['Книга 1'] });
      return real(...args);
    });
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    expect(await within(window).findByText(said, undefined, { timeout: 10_000 })).toBeTruthy();
    for (const exit of exits) expect(within(window).getByRole('button', { name: exit })).toBeTruthy();
    if (exits.length) {
      fireEvent.click(within(window).getByRole('button', { name: updateWords.readAgain }));
      await waitFor(() => expect(update.mock.calls.filter((call) => (call[2] as Body).mode === 'preview')).toHaveLength(2));
      const [first, second] = update.mock.calls.filter((call) => (call[2] as Body).mode === 'preview').map((call) => (call[3] as { idempotencyKey: string }).idempotencyKey);
      expect(first).not.toBe(second);
    }
  });

  it('waits for another reading of the folder and repeats, six tries in all, then says it with «Повторить»', { timeout: 40_000 }, async () => {
    const { real, update } = await theShelf();
    let first = true;
    update.mockImplementation(async (...args) => {
      if (first) {
        first = false;
        throw new MockApiError(429, 'another operation reads a vault', 'updateBookFromObsidian', 'obsidian.busy', { retryAfter: 1 });
      }
      return real(...args);
    });
    let window = await openWindow();
    expect(await within(window).findByText(words.busy(1), undefined, { timeout: 10_000 })).toBeTruthy();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));

    update.mockReset().mockRejectedValue(new MockApiError(429, 'another operation reads a vault', 'updateBookFromObsidian', 'obsidian.busy', { retryAfter: 0.01 }));
    window = await openWindow();
    expect(await within(window).findByText(words.busyEnd, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(update).toHaveBeenCalledTimes(6);
    expect(within(window).getByRole('button', { name: words.retry })).toBeTruthy();
  });

  it('does not repeat a reading the author left while it waited: the reading of the window opened again goes alone', { timeout: 40_000 }, async () => {
    const { real, update } = await theShelf();
    let first = true;
    update.mockImplementation(async (...args) => {
      if (first) {
        first = false;
        throw new MockApiError(429, 'another operation reads a vault', 'updateBookFromObsidian', 'obsidian.busy', { retryAfter: 1 });
      }
      return real(...args);
    });
    let window = await openWindow();
    expect(await within(window).findByText(words.busy(1), undefined, { timeout: 10_000 })).toBeTruthy();
    const left = keysOf(update)[0]![1];
    fireEvent.click(within(window).getByRole('button', { name: words.cancel }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull());
    window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    // The wait of the reading left is over: it asks nothing.
    await new Promise((resolve) => setTimeout(resolve, 1_500));
    expect(keysOf(update).filter(([, key]) => key === left)).toHaveLength(1);
    expect(keysOf(update).map(([mode]) => mode)).toEqual(['preview', 'preview']);
  });

  it('says a failure of ours with «Повторить»', { timeout: 40_000 }, async () => {
    const { update } = await theShelf();
    update.mockRejectedValue(problem(500, 'server.error'));
    const window = await openWindow();
    expect(await within(window).findByText(words.previewFailed, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(within(window).getByRole('button', { name: words.retry })).toBeTruthy();
  });
});

// OU2.6, OU3.2 (е): «Обновить книгу» reads the folder again, then «Книги» lead the update — its line in work, the line of its
// result with «Что изменилось», «Вернуть»; after it no line of «Книги», the shell and the windows names a file or a site.
describe('the update from the folder', () => {
  it('reads again with the button busy, then «Книги» say it in work and its result; no file and no site anywhere after it', { timeout: 60_000 }, async () => {
    const shelf = await theShelf();
    const { api } = shelf;
    const window = await openWindow();
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    const held = holdAnswer(shelf, 'apply');
    // The Job of the update goes until the line of the work is seen.
    const getJob = api.getJob.bind(api);
    let going = true;
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      return going && job.result?.type === 'import' && job.result.importMode === 'update_book' ? { ...job, status: 'running', progress: 0.5 } : job;
    });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    await waitFor(() => expect(within(window).getByRole('button', { name: updateWords.confirm }).getAttribute('aria-busy')).toBe('true'));
    expect(within(window).getAllByRole('status').some((line) => line.textContent?.includes(updateWords.reading))).toBe(true);
    held.release();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });
    expect(await screen.findByText(updateWords.working('Застава'), undefined, { timeout: 10_000 })).toBeTruthy();
    going = false;
    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» обновлена из Obsidian: изменена 1 глава'), { timeout: 15_000 });
    expect(screen.queryByText(updateWords.working('Застава'))).toBeNull();

    const fileOrSite = /файл|сайт/iu;
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Застава' })).getByText(/Обновлена /u)).toBeTruthy());
    expect(document.body.textContent).not.toMatch(fileOrSite);
    const toast = screen.getAllByRole('status').find((line) => line.textContent?.includes('обновлена из Obsidian'))!;
    fireEvent.click(within(toast).getByRole('button', { name: 'Что изменилось' }));
    const changes = await screen.findByRole('dialog', { name: 'Что изменилось в «Застава»' });
    await waitFor(() => expect(changes.textContent).toMatch(/Обновлена /u));
    expect(document.body.textContent).not.toMatch(fileOrSite);
    fireEvent.click(within(changes).getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    const question = await screen.findByRole('dialog', { name: 'Вернуть «Застава» к версии до обновления?' });
    expect(document.body.textContent).not.toMatch(fileOrSite);
    fireEvent.click(within(question).getByRole('button', { name: 'Вернуть версию до обновления' }));
    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» возвращена к версии до обновления'));
    fireEvent.click(within(changes).getAllByRole('button', { name: 'Закрыть' })[0]!);
    fireEvent.click(screen.getByRole('button', { name: 'Недавно удалённые' }));
    await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    expect(document.body.textContent).not.toMatch(fileOrSite);
  });
});
