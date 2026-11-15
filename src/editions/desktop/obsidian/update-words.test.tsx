// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient } from '@tanstack/react-query';
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock/scenarios';
import type { ProjectJob } from '../../../entities/manuscript/api';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { obsidianKeys } from './obsidian-api';
import { updateWords } from './words';
import { aBookOfItsFolder, loadFolders, menuOf, renderInTheApp, settle, toastsText, type TheApp } from './obsidian.test-helpers';

// The root of these tests is the edition of the desktop app, as `npm run build:desktop` makes it.
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

/** The update of a book by a file, its Job as the mock starts it. */
function updateByFile(api: TheApp, saga: string, bookId: string, key: string) {
  return api.importBookFile(saga, {
    file: new File(['<FictionBook>том</FictionBook>'], 'tom.fb2', { type: 'application/x-fictionbook+xml' }),
    options: { importMode: 'update_book', bookId },
  }, { idempotencyKey: key });
}

/** Holds a Job of the mock as the test says: the page reads it so, and the mock does not work it. */
function holdJob(api: TheApp, started: ProjectJob, held: (job: ProjectJob) => ProjectJob) {
  const getJob = api.getJob.bind(api);
  return vi.spyOn(api, 'getJob').mockImplementation(async (id) => (id === started.id ? held(started) : getJob(id)));
}

/** The error of a Job that failed with a code. */
function failure(code: string) {
  return { type: 'about:blank', title: 'Job failed', status: 500, code, requestId: 'req-1' };
}

/** «Обновлена 16 июня», «Обновлена сегодня в 14:20» — not «Обновлена из файла». */
const updatedLine = /Обновлена (?!из )[^·.]+/u;

const fileOrSite = /файл|сайт/iu;

// specs/2026-11-15-desk-obsidian-book-web.md OU3.2 through the edition of the app (`wrapPages` over the pages and the shell):
// where the app does not know where an update came from, its words do not name the file; there is no site in the app.
describe('the words of the app about the update of a book', () => {
  it('say the line of a book, «Что изменилось», the question of the return and «Обновления книг» without the file', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    await updateByFile(api, saga, bookId, 'idem-words-update-01');
    await settle(api, saga);
    // The return takes back a chapter the update added: the question says who added it.
    const preview = api.previewBookUpdateRevert.bind(api);
    vi.spyOn(api, 'previewBookUpdateRevert').mockImplementation(async (id) => ({
      ...await preview(id), removed: [{ id: 'chapter-9', title: 'Ночной дозор' }],
    }));
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);

    const card = await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    await waitFor(() => expect(card.textContent).toMatch(new RegExp(`${updatedLine.source} · Что изменилось`, 'u')));
    expect(card.textContent).not.toMatch(fileOrSite);
    const menu = menuOf('Застава');
    const changes = within(menu).getByRole('menuitem', { name: /^Что изменилось/u });
    expect(changes.textContent).toMatch(new RegExp(`^Что изменилось${updatedLine.source}$`, 'u'));
    fireEvent.click(changes);

    const window = await screen.findByRole('dialog', { name: 'Что изменилось в «Застава»' });
    await waitFor(() => expect(window.textContent).toMatch(updatedLine));
    expect(window.textContent).not.toMatch(fileOrSite);
    fireEvent.click(within(window).getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    const question = await screen.findByRole('dialog', { name: 'Вернуть «Застава» к версии до обновления?' });
    expect(question.textContent).toContain('В «Недавно удалённые» уйдёт глава «Ночной дозор» — её добавило обновление.');
    expect(question.textContent).not.toMatch(fileOrSite);
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    fireEvent.click(within(window).getAllByRole('button', { name: 'Закрыть' })[0]!);

    fireEvent.click(screen.getByRole('button', { name: 'Недавно удалённые' }));
    const trash = await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    const updates = await within(trash).findByRole('region', { name: 'Обновления книг' });
    expect(within(updates).getByText('Книга «Застава» обновлена')).toBeTruthy();
    expect(updates.textContent).not.toMatch(fileOrSite);
  });

  it('say why «Вернуть прежнюю версию…» waits while the book is updated, without the file', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    await updateByFile(api, saga, bookId, 'idem-words-update-07');
    await settle(api, saga);
    // Another update of the book goes on at the server when the author asks for the return.
    vi.spyOn(api, 'previewBookUpdateRevert').mockRejectedValue(new MockApiError(409, 'busy', 'previewBookUpdateRevert', 'book.busy'));
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    fireEvent.click(await waitFor(() => within(menuOf('Застава')).getByRole('menuitem', { name: /^Что изменилось/u })));
    const window = await screen.findByRole('dialog', { name: 'Что изменилось в «Застава»' });
    fireEvent.click(await within(window).findByRole('button', { name: 'Вернуть прежнюю версию…' }));
    await waitFor(() => expect(toastsText()).toContain('Книга сейчас обновляется — вернуть прежнюю версию можно будет после этого.'));
    expect(toastsText()).not.toMatch(fileOrSite);
  });

  it('keep the words of the app and give no item of a folder when the folder of the saga does not read', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    await updateByFile(api, saga, bookId, 'idem-words-update-08');
    await settle(api, saga);
    const vault = vi.spyOn(api, 'getObsidianVault').mockRejectedValue(new MockApiError(500, 'failed', 'getObsidianVault', 'server.error'));
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    const card = await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    await waitFor(() => expect(vault).toHaveBeenCalled());
    await waitFor(() => expect(card.textContent).toMatch(new RegExp(`${updatedLine.source} · Что изменилось`, 'u')));
    expect(card.textContent).not.toMatch(fileOrSite);
    const menu = menuOf('Застава');
    expect(within(menu).getByRole('menuitem', { name: /^Что изменилось/u }).textContent).toMatch(new RegExp(`^Что изменилось${updatedLine.source}$`, 'u'));
    expect(within(menu).getByRole('menuitem', { name: /^Новая версия из файла…/u })).toBeTruthy();
    expect(within(menu).queryByRole('menuitem', { name: updateWords.updateItem })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: updateWords.linkItem })).toBeNull();
    fireEvent.click(within(menu).getByRole('button', { name: 'Закрыть меню', hidden: true }));
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
  });

  it('say a book being updated: its mark, the reasons of its menu and of «Добавить книги»', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    const update = await updateByFile(api, saga, bookId, 'idem-words-update-02');
    holdJob(api, update.job, (job) => ({ ...job, status: 'running', progress: 0.4 }));
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);

    const card = await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    await waitFor(() => expect(within(card).getByText('Обновляется')).toBeTruthy());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-description')).toBe('Дождитесь, пока книга обновится.'));
    const menu = menuOf('Застава');
    expect(within(menu).getByRole('menuitem', { name: /^Новая версия из файла…/u }).textContent).toContain('Книга уже обновляется.');
    expect(menu.textContent).toContain('Пока книга обновляется, удалить её нельзя.');
    fireEvent.click(within(menu).getByRole('button', { name: 'Закрыть меню', hidden: true }));
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
  });

  it('say the result of an update of this window from a folder «из Obsidian», and its failure with the repeat from the folder', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    const fromFolder = await updateByFile(api, saga, bookId, 'idem-words-update-03');
    const failedFromFolder = await updateByFile(api, saga, bookId, 'idem-words-update-04');
    // Both go until the shell follows them; then one ends as the mock works it, the other fails.
    let going = true;
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      if (going && (id === fromFolder.job.id || id === failedFromFolder.job.id)) return { ...(id === fromFolder.job.id ? fromFolder : failedFromFolder).job, status: 'running' };
      if (id === failedFromFolder.job.id) return { ...failedFromFolder.job, status: 'failed', result: null, error: failure('server.error') };
      return getJob(id);
    });
    // «Обновить книгу» of the window from the folder in the cache of the mutations of this window — twice.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    for (const started of [fromFolder, failedFromFolder]) {
      await client.getMutationCache().build(client, { mutationKey: obsidianKeys.update, mutationFn: async () => started })
        .execute({ projectId: saga, bookId });
    }
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`, { client });
    const card = await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    await waitFor(() => expect(within(card).getByText('Обновляется')).toBeTruthy());
    going = false;

    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» обновлена из Obsidian: изменена 1 глава'), { timeout: 15_000 });
    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» не обновилась — повторите «Обновить из Obsidian…» в меню книги'), { timeout: 15_000 });
    expect(toastsText()).not.toMatch(fileOrSite);
  });

  it('say the result of an update the window did not start without its source, though the book holds a folder', { timeout: 30_000 }, async () => {
    const { api, saga, bookId } = await aBookOfItsFolder();
    // Updates the window did not start (the app restarted while they went): one ends, one fails.
    const done = await updateByFile(api, saga, bookId, 'idem-words-update-05');
    const failed = await updateByFile(api, saga, bookId, 'idem-words-update-06');
    let going = true;
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      if (going && (id === done.job.id || id === failed.job.id)) return { ...(id === done.job.id ? done : failed).job, status: 'running' };
      if (id === failed.job.id) return { ...failed.job, status: 'failed', result: null, error: failure('server.error') };
      return getJob(id);
    });
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    const card = await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    await waitFor(() => expect(within(card).getByText('Обновляется')).toBeTruthy());
    going = false;
    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» обновлена: изменена 1 глава'), { timeout: 15_000 });
    await waitFor(() => expect(toastsText()).toContain(updateWords.failedUnknown('Книга «Застава»')), { timeout: 15_000 });
    expect(updateWords.failedUnknown('Книга «Застава»')).toBe('Книга «Застава» не обновилась — повторите обновление в меню книги');
    expect(toastsText()).not.toMatch(/файл|сайт|Obsidian/iu);
  });

  it.each([
    ['import.too_large', 'В файле больше 10 миллионов знаков — столько в одну книгу не поместить. Разделите его на тома.'],
    ['server.error', 'Это сбой на нашей стороне — повторите: «Новая версия из файла…» в меню книги.'],
  ] as const)('say an update from a file of this window in the words of the file, of a book that holds a folder too: %s', { timeout: 40_000 }, async (code, reason) => {
    const { api, saga } = await aBookOfItsFolder();
    // The first update from the file ends; the second fails with the code.
    const updates: string[] = [];
    const importBookFile = api.importBookFile.bind(api);
    vi.spyOn(api, 'importBookFile').mockImplementation(async (...args) => {
      const answer = await importBookFile(...args);
      if (args[1].options?.importMode === 'update_book') updates.push(answer.job.id);
      return answer;
    });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      return updates.indexOf(id) === 1 ? { ...job, status: 'failed', result: null, canCancel: false, error: failure(code) } as ProjectJob : job;
    });
    const view = renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    for (const round of [1, 2]) {
      fireEvent.click(within(menuOf('Застава')).getByRole('menuitem', { name: 'Новая версия из файла…' }));
      const input = view.container.querySelector<HTMLInputElement>('input[type="file"][hidden]')!;
      fireEvent.change(input, { target: { files: [new File(['<FictionBook>новый том</FictionBook>'], `том-${round}.fb2`, { type: 'application/x-fictionbook+xml' })] } });
      const window = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
      await within(window).findByRole('heading', { name: 'Что изменится в «Застава»' }, { timeout: 15_000 });
      fireEvent.click(within(window).getByRole('button', { name: 'Обновить книгу' }));
      if (round === 1) await waitFor(() => expect(toastsText()).toContain('Книга «Застава» обновлена: изменена 1 глава'), { timeout: 20_000 });
      await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull(), { timeout: 15_000 });
      await waitFor(() => expect(screen.queryByText(/^Обновляем «Застава» из файла/u)).toBeNull(), { timeout: 20_000 });
    }
    await waitFor(() => expect(toastsText()).toContain('Книга «Застава» не обновилась.'), { timeout: 20_000 });
    expect(toastsText()).toContain(reason);
    expect(toastsText()).not.toContain('Obsidian');
    expect(toastsText()).not.toContain(updateWords.failedUnknown('Книга «Застава»'));
  });

  it('keep the file in the window of a new version from a file, with the edits in the app and no forecast', { timeout: 30_000 }, async () => {
    const { api, saga } = await aBookOfItsFolder();
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      if (job.result?.type !== 'import' || !job.result.preview) return job;
      return { ...job, result: { ...job.result, preview: { ...job.result.preview, chapters: job.result.preview.chapters.map((chapter) => ({ ...chapter, editedOnSite: true })) } } };
    });
    const view = renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    fireEvent.click(within(menuOf('Застава')).getByRole('menuitem', { name: 'Новая версия из файла…' }));
    const input = view.container.querySelector<HTMLInputElement>('input[type="file"][hidden]')!;
    fireEvent.change(input, { target: { files: [new File(['<FictionBook>новый том</FictionBook>'], 'том-2.fb2', { type: 'application/x-fictionbook+xml' })] } });
    const window = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    await within(window).findByRole('heading', { name: 'Что изменится в «Застава»' }, { timeout: 15_000 });
    expect(window.textContent).toContain('Правки, сделанные в приложении в этих главах, заменятся текстом файла: «');
    expect(window.textContent).toContain(', есть правки в приложении');
    expect(window.textContent).toContain('Если файл окажется не тот, прежнюю версию книги можно вернуть 7 дней');
    expect(window.textContent).not.toMatch(/сайт|Помощник перечитает|Перечитывать главы/u);
  });

  it('do not name a book of a folder by a file in «Как разобралась книга»; a book of a file is named so', { timeout: 30_000 }, async () => {
    const { api, saga } = await aBookOfItsFolder();
    // «Книга 3» of the example gives the book «Книга 3» — the name of the file of its volume.
    await loadFolders(api, saga, ['Книга 3'], 'idem-words-books-03');
    const byFile = await api.importBookFile(saga, {
      file: new File(['<FictionBook>том</FictionBook>'], 'tom_7.fb2', { type: 'application/x-fictionbook+xml' }), metadata: { sourceFileName: 'tom_7.fb2' },
    }, { idempotencyKey: 'idem-words-file-07' });
    expect(byFile.job.id).toBeTruthy();
    await settle(api, saga);
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);

    await screen.findByRole('article', { name: 'Книга 3' }, { timeout: 15_000 });
    await waitFor(() => expect(within(menuOf('Книга 3')).getByRole('menuitem', { name: 'Как разобралась книга' })).toBeTruthy());
    fireEvent.click(within(screen.getByRole('menu', { name: 'Действия книги' })).getByRole('menuitem', { name: 'Как разобралась книга' }));
    const ofFolder = await screen.findByRole('dialog', { name: 'Как разобралась книга «Книга 3»' });
    await within(ofFolder).findByText(/так вы будете читать книгу/u);
    await waitFor(() => expect(ofFolder.textContent).not.toContain('по имени файла'));
    fireEvent.click(within(ofFolder).getAllByRole('button', { name: 'Закрыть' })[0]!);

    const book = (await api.listBooks(saga)).data.find((one) => one.title === 'tom_7')!;
    await screen.findByRole('article', { name: book.title });
    fireEvent.click(within(menuOf(book.title)).getByRole('menuitem', { name: 'Как разобралась книга' }));
    const ofFile = await screen.findByRole('dialog', { name: `Как разобралась книга «${book.title}»` });
    expect(await within(ofFile).findByText(`Книга названа по имени файла: «${book.title}».`)).toBeTruthy();
  });
});
