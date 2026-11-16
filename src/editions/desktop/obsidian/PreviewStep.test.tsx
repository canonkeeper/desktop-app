// SPDX-License-Identifier: AGPL-3.0-only
import { focusManager, QueryClient } from '@tanstack/react-query';
import { act, cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import type { ChoiceMemory, FolderChoice } from './choice';
import { keepChoice } from './choice-memory';
import type { ObsidianPreviewAnswer, ObsidianRecordPreview } from './obsidian-api';
import { axeViolations, renderAt, theApp, until, withoutShell, withShell, type TheApp } from './obsidian.test-helpers';
import { amountWords, bookMeta, words } from './words';

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
  'Книга 1': 'skip', 'Книга 2': 'skip', 'Книга 3': 'skip', 'Книга 4': 'skip', 'Книга 5': 'skip', 'Книга 5/Герои': 'skip', Персонажи: 'skip', Места: 'skip', Шаблоны: 'skip',
};

/** A linked saga whose author chose these folders, and its page of the bridge at the choice. */
async function chosen(kinds: Record<string, FolderChoice>, scenario: Parameters<typeof theApp>[0] = {}, notes: string[] = []) {
  const app = await theApp(scenario);
  await app.api.beginObsidianVaultChoice(app.saga);
  withShell();
  const memory: ChoiceMemory = { kinds: { ...nothingElse, ...kinds }, notes };
  keepChoice('user-local', app.saga, memory);
  return app;
}

async function showPreview(api: TheApp, saga: string, client?: QueryClient) {
  const view = renderAt(api, `/manuscript/obsidian?projectId=${saga}`, client ? { client } : {});
  fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
  await screen.findByRole('heading', { name: words.previewTitle }, { timeout: 10_000 });
  return view;
}

const card = (title: string) => screen.findByRole('listitem', { name: title }, { timeout: 10_000 });
const loadButton = () => screen.getByRole('button', { name: words.load });

/** The preview of the mock with its answer changed by the test. */
function answerWith(api: TheApp, change: (answer: ObsidianPreviewAnswer) => ObsidianPreviewAnswer) {
  const real = api.previewObsidianImport.bind(api);
  return vi.spyOn(api, 'previewObsidianImport').mockImplementation(async (projectId, body) => change(await real(projectId, body)));
}

const record = (name: string, extra: Partial<ObsidianRecordPreview> = {}): ObsidianRecordPreview => ({
  path: `Герои/${name}.md`, folder: 'Герои', name, kind: 'character', state: 'new', changed: false, aliases: [], properties: 0, warnings: [],
  cut: 0, left: 0, seen: '0123456789abcdef', replaceable: false, ...extra,
});

// specs/2026-11-15-desk-obsidian-web.md OW3.1: the preview is read only by a press.
describe('the reading of «что получится»', () => {
  it('reads only by a press: not on the return to the window, to the step, on a change of the choice, on a reload', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    const previews = vi.spyOn(api, 'previewObsidianImport');
    const view = renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    await card('Книга 3');
    expect(previews).toHaveBeenCalledTimes(1);
    act(() => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
    });
    fireEvent.click(screen.getByRole('button', { name: words.changeChoice }));
    await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') });
    fireEvent.click(screen.getByRole('button', { name: new RegExp(`^${words.choiceOf('Книга 4')}`, 'u') }));
    fireEvent.click(await screen.findByRole('option', { name: new RegExp(`^${words.book}`, 'u') }));
    act(() => view.go(`/manuscript/obsidian?projectId=${saga}&step=preview`));
    expect(await screen.findByText(words.staleChoice)).toBeTruthy();
    expect(reasonOf(loadButton())).toBe(words.whyStale);
    expect(previews).toHaveBeenCalledTimes(1);
    cleanup();

    // A reload on «что получится»: the cache of the page is empty, nothing is read, the step is the choice.
    const reloaded = renderAt(api, `/manuscript/obsidian?projectId=${saga}&step=preview`);
    await until(() => expect(reloaded.steps.at(-1)).toEqual({ path: '/manuscript/obsidian', query: { projectId: saga }, options: { replace: true } }));
    expect(previews).toHaveBeenCalledTimes(1);
  });

  it('says it reads the notes, and a page left meanwhile finds the preview read', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    let answer: () => void = () => undefined;
    const real = api.previewObsidianImport.bind(api);
    const previews = vi.spyOn(api, 'previewObsidianImport').mockImplementation((projectId, body) => new Promise((resolve) => { answer = () => resolve(real(projectId, body)); }));
    const view = await showPreview(api, saga);
    // The line of the step is a status of its own (the part of the export under the step has its own).
    const status = screen.getByText(words.readingNotes).closest('[role="status"]')!;
    expect(status.textContent).toContain(words.readingNotes);
    expect(status.textContent).toContain(words.readingLong);
    view.unmount();
    answer();
    renderAt(api, `/manuscript/obsidian?projectId=${saga}&step=preview`, { client: view.client });
    expect(await card('Книга 3')).toBeTruthy();
    expect(previews).toHaveBeenCalledTimes(1);
  });
});

// OW3.2: a card of a book in each state with its buttons; the warnings, what did not go in, the notes.
describe('the books of «что получится»', () => {
  it('says a new book with its warnings, what did not go in, its notes and «Не брать» of a note; a book that does not load', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 2': 'book', 'Книга 3': 'book', 'Книга 4': 'book', Шаблоны: 'book' });
    const { container } = await showPreview(api, saga);
    const first = await card('Застава');
    expect(within(first).getByText(bookMeta(3, 73))).toBeTruthy();
    expect(within(first).getByText(words.stNew)).toBeTruthy();
    expect(within(first).getByText(words.wCount(3, 2, ['Переправа']))).toBeTruthy();
    expect(within(first).getByText('Не вошло в текст: сноски — 1, вставки других заметок — 1, комментарии — 2')).toBeTruthy();
    expect(within(first).getByText('Пропущено при чтении: 4 заметки')).toBeTruthy();
    fireEvent.click(within(first).getByText(words.notesToggle(2)));
    expect(within(first).getByText(`${words.chapter(1, 'Пролог')} · ${words.orderIndex}`)).toBeTruthy();
    expect(within(first).getByRole('button', { name: `${words.skip} «Переправа»` })).toBeTruthy();
    expect(within(await card('Книга 2')).getByText(words.wOne('Том'))).toBeTruthy();
    expect(within(await card('Книга 3')).getByText(words.wOrder)).toBeTruthy();
    expect(screen.getByText(words.wLong)).toBeTruthy();
    expect(within(await card('Шаблоны')).getByText(words.stCant('в заметках папки не нашлось текста глав'))).toBeTruthy();
    expect(await axeViolations(container)).toEqual([]);

    // «Не брать» of a note makes the preview old until «Показать заново».
    fireEvent.click(within(first).getByRole('button', { name: `${words.skip} «Переправа»` }));
    expect(screen.getByText(words.staleChoice)).toBeTruthy();
    expect(reasonOf(loadButton())).toBe(words.whyStale);
  });

  it('lists the notes left out by «Не брать» with «Вернуть»: nothing is lost in silence, the way back is the same change of the choice', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book' });
    await showPreview(api, saga);
    const first = await card('Застава');
    fireEvent.click(within(first).getByText(words.notesToggle(2)));
    fireEvent.click(within(first).getByRole('button', { name: `${words.skip} «Переправа»` }));
    // At once: the note stands among those not taken.
    const left = () => screen.getByText(words.notTaken(1)).closest('details') as HTMLElement;
    expect(within(left()).getByText('02 Переправа')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: words.showAgain }));
    await until(() => expect(screen.queryByText(words.staleChoice)).toBeNull());
    const again = await card('Застава');
    fireEvent.click(within(again).getByText(words.notesToggle(1)));
    expect(within(again).queryByText('Переправа')).toBeNull();
    fireEvent.click(within(left()).getByRole('button', { name: `${words.giveBack} «02 Переправа»` }));
    expect(screen.getByText(words.staleChoice)).toBeTruthy();
    expect(screen.queryByText(words.notTaken(1))).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: words.showAgain }));
    await until(() => expect(screen.queryByText(words.staleChoice)).toBeNull());
    fireEvent.click(within(await card('Застава')).getByText(words.notesToggle(2)));
    expect(within(await card('Застава')).getByText('Переправа')).toBeTruthy();
  });

  it('says the books of the saga: unchanged, changed (named by the button), in the trash with its ways, loading', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 2': 'book', 'Книга 3': 'book' }, { obsidianLinked: true });
    const books = await api.previewObsidianImport(saga, { books: ['Книга 3'] });
    await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 3', fingerprint: books.books[0]!.fingerprint }] }, { idempotencyKey: 'idem-books-preview' });
    await showPreview(api, saga);
    const changed = await card('Застава');
    expect(within(changed).getByText(words.stChanged('Застава'))).toBeTruthy();
    fireEvent.click(within(changed).getByText(words.notesToggle(2)));
    expect(within(changed).queryByRole('button', { name: new RegExp(`^${words.skip}`, 'u') })).toBeNull();
    expect(within(changed).getAllByText(words.fixed).length).toBe(2);
    expect(screen.getAllByText(words.notUpdated('Застава')).length).toBe(1);
    const trash = await card('Книга 2');
    expect(within(trash).getByText(words.stTrash('Белый порт'))).toBeTruthy();
    expect(within(trash).getByRole('button', { name: words.openTrash })).toBeTruthy();
    fireEvent.click(within(trash).getByRole('button', { name: words.unlinkBook }));
    expect(await screen.findByText(words.unlinkedBook('Белый порт'))).toBeTruthy();
    expect(await screen.findByText(words.staleBooks)).toBeTruthy();
    const loading = await card('Книга 3');
    expect(within(loading).getByText(words.stLoading)).toBeTruthy();
    expect(within(loading).getByRole('button', { name: words.toBooks })).toBeTruthy();
  });

  it('offers «Связать с «…»» for a book of the saga whose folder is not in its place — a folder renamed is no second book', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book' });
    const outpost = await api.createBook(saga, { title: 'Застава' });
    const real = api.getObsidianVault.bind(api);
    vi.spyOn(api, 'getObsidianVault').mockImplementation(async (projectId) => ({
      ...(await real(projectId)), books: [{ bookId: outpost.id, title: 'Застава', folder: 'Книги/Застава', loadedAt: null, presence: 'missing', excluded: [], nested: [] }],
    }));
    const linked = vi.spyOn(api, 'linkBookToObsidianFolder');
    await showPreview(api, saga);
    const like = await card('Застава');
    expect(within(like).getByText(words.stSimilarMissing('Застава'))).toBeTruthy();
    fireEvent.click(within(like).getByRole('button', { name: words.linkSimilar('Застава') }));
    await until(() => expect(linked).toHaveBeenCalledWith(saga, outpost.id, { folder: 'Книга 1' }));
  });

  it('is old once the live books of the saga changed after it was read: a book given back from «Недавно удалённые»', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    const book = await api.createBook(saga, { title: 'Весна и осень' });
    await api.linkBookToObsidianFolder(saga, book.id, { folder: 'Книга 3' });
    await api.deleteBook(book.id);
    const view = await showPreview(api, saga);
    expect(within(await card('Книга 3')).getByText(words.stTrash('Весна и осень'))).toBeTruthy();
    expect(screen.queryByText(words.staleBooks)).toBeNull();
    // «Открыть «Недавно удалённые»» → «Вернуть»: the shelf of the saga reads its books again.
    await api.restoreBook(book.id);
    await act(() => view.client.refetchQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: saga }) }));
    expect(await screen.findByText(words.staleBooks, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(reasonOf(loadButton())).toBe(words.whyStale);
  }, 20_000);

  it('says a failure of an action of a card by «Не получилось — попробуйте ещё раз»', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 2': 'book' }, { obsidianLinked: true });
    vi.spyOn(api, 'unlinkBookFromObsidianFolder').mockRejectedValue(new MockApiError(500, 'failure', 'unlinkBookFromObsidianFolder', 'server.error'));
    await showPreview(api, saga);
    fireEvent.click(within(await card('Книга 2')).getByRole('button', { name: words.unlinkBook }));
    expect(await screen.findByText(words.tryAgain)).toBeTruthy();
    expect(screen.queryByText(words.cantOther)).toBeNull();
  });

  it('closes no button of a card without its reason while an action of a card goes: the pressed one says it works', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 2': 'book' }, { obsidianLinked: true });
    vi.spyOn(api, 'unlinkBookFromObsidianFolder').mockImplementation(() => new Promise(() => undefined));
    const { container } = await showPreview(api, saga);
    const trash = await card('Книга 2');
    fireEvent.click(within(trash).getByRole('button', { name: words.unlinkBook }));
    await until(() => expect(within(trash).getByRole('button', { name: words.unlinkBook }).getAttribute('aria-busy')).toBe('true'));
    const closed = [...container.querySelectorAll('button')].filter((button) => (button.disabled || button.getAttribute('aria-disabled') === 'true')
      && button.getAttribute('aria-busy') !== 'true' && !button.getAttribute('aria-description'));
    expect(closed.map((button) => button.textContent)).toEqual([]);
    expect(within(trash).getByRole('button', { name: words.openTrash })).toBeTruthy();
  });

  it('offers «Связать с «…»» for a book of the saga with a title like this one, and says a book of the saga that does not glue', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book' });
    const outpost = await api.createBook(saga, { title: 'Застава' });
    const linked = vi.spyOn(api, 'linkBookToObsidianFolder');
    await showPreview(api, saga);
    const like = await card('Застава');
    expect(within(like).getByText(words.stSimilar('Застава'))).toBeTruthy();
    fireEvent.click(within(like).getByRole('button', { name: words.linkSimilar('Застава') }));
    expect(await screen.findByText(words.linkedDone('Застава', 'Книга 1'))).toBeTruthy();
    expect(linked).toHaveBeenCalledWith(saga, outpost.id, { folder: 'Книга 1' });
    expect(await screen.findByText(words.staleBooks)).toBeTruthy();
    cleanup();

    const broken = await chosen({ 'Книга 1': 'book' }, { obsidianLinked: true });
    answerWith(broken.api, (answer) => ({ ...answer, books: answer.books.map((book) => ({ ...book, refusal: 'import.too_large' })) }));
    await showPreview(broken.api, broken.saga);
    expect(within(await card('Застава')).getByText(words.stBroken('Застава', 'в заметках больше 10 миллионов знаков — разделите папку на несколько книг'))).toBeTruthy();
  });
});

// OW3.3: the records of «что получится».
describe('the records of «что получится»', () => {
  it('says every state, takes decisions only where the server does, «Для всех …», the held name and the deadlock of a swap', async () => {
    const { api, saga } = await chosen({ Персонажи: 'materials:character', Места: 'materials:place' }, { obsidianLinked: true });
    answerWith(api, (answer) => ({
      ...answer, materials: {
        ...answer.materials, records: [
          ...answer.materials.records,
          record('Тим', { path: 'Герои/Тим.md', state: 'other_source', heldBy: 'Герои/Сава.md' }),
          record('Сава', { path: 'Герои/Сава.md', state: 'other_source', heldBy: 'Герои/Тим.md' }),
          record('Ирвин', { state: 'edited_in_app' }),
          record('Морской устав', { state: 'other_source', replaceable: true }),
        ],
      },
    }));
    const { container } = await showPreview(api, saga);
    const list = await screen.findByRole('list', { name: words.records }, { timeout: 10_000 });
    expect(within(list).getByText(words.rNewIn('Ольга'))).toBeTruthy();
    expect(within(list).getByText(words.rNewIn('Мирон'))).toBeTruthy();
    expect(within(list).getAllByText(words.rNew).length).toBeGreaterThan(0);
    expect(within(list).getByText(words.rSame)).toBeTruthy();
    expect(within(list).getByText(words.rUpd)).toBeTruthy();
    expect(within(list).getAllByText(words.rEdited).length).toBe(2);
    expect(within(list).getByText(words.rTaken('Морской устав'))).toBeTruthy();
    expect(within(list).getByText(words.rHeld('Йорген', 'Йорген'))).toBeTruthy();
    expect(within(list).getAllByText(words.deadlock('Тим', 'Сава')).length + within(list).getAllByText(words.deadlock('Сава', 'Тим')).length).toBe(2);
    expect(screen.getByText(words.rwSections)).toBeTruthy();
    expect(screen.getByText(words.rwList)).toBeTruthy();
    expect(screen.getByText(words.rwCut)).toBeTruthy();
    expect(screen.getByText(words.rwProps(3))).toBeTruthy();
    // Decisions: the two records edited in the app and the name a record keeps — «Не менять» by default.
    expect(within(list).getAllByRole('group').map((group) => group.getAttribute('aria-label'))).toEqual(['Йорген', 'Ирвин', 'Морской устав']);
    expect(within(list).getAllByRole('radio', { name: words.keep }).every((radio) => (radio as HTMLInputElement).checked)).toBe(true);
    expect(screen.getByText(words.forEdited(2))).toBeTruthy();
    expect(screen.queryByText(/Для всех с занятым именем/u)).toBeNull();
    expect(screen.getByText('4 новых · 1 обновится · 1 без изменений · 2 правлены в приложении · 4 с занятым именем')).toBeTruthy();
    expect(await axeViolations(container)).toEqual([]);
  });

  it('shows the records by portions and finds a record by its name from 30', async () => {
    const { api, saga } = await chosen({ Персонажи: 'materials:character' });
    answerWith(api, (answer) => ({ ...answer, materials: { ...answer.materials, records: Array.from({ length: 70 }, (_, index) => record(`Герой ${index + 1}`)) } }));
    await showPreview(api, saga);
    const list = await screen.findByRole('list', { name: words.records }, { timeout: 10_000 });
    expect(within(list).getAllByRole('listitem').length).toBe(50);
    fireEvent.click(screen.getByRole('button', { name: words.more }));
    expect(within(list).getAllByRole('listitem').length).toBe(70);
    fireEvent.change(screen.getByRole('searchbox', { name: words.find }), { target: { value: 'Герой 7' } });
    expect(within(list).getAllByRole('listitem').length).toBe(2);
  });
});

// OW3.5, OW3.6: the word of the reading, what «Загрузить» loads and what it does not.
describe('the bottom of «что получится»', () => {
  it.each([
    [{ helper: 'none', consent: false }, words.readNone],
    [{ helper: 'own', consent: true }, words.readNow(amountWords(48, 310_000))],
    [{ helper: 'account', consent: false }, words.readLater(amountWords(48, 310_000))],
  ] as const)('says the reading by who reads and the consent: %j', async (reading, said) => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    answerWith(api, (answer) => ({ ...answer, reading: { ...reading, newChapters: 48, newCharacters: 310_000 } }));
    const { container } = await showPreview(api, saga);
    expect(await screen.findByText(said)).toBeTruthy();
    expect(container.textContent).not.toMatch(/(?<!\p{L})(?:ИИ|модел|токен)/iu);
  });

  it('says why nothing loads when the new books would not: their reasons are on their cards', async () => {
    const { api, saga } = await chosen({ Шаблоны: 'book' });
    await showPreview(api, saga);
    expect(within(await card('Шаблоны')).getByText(words.stCant('в заметках папки не нашлось текста глав'))).toBeTruthy();
    expect(reasonOf(loadButton())).toBe(words.nothingRefused);
  });

  it('has no line of the reading without chapters, says what loads, and closes «Загрузить» when nothing would', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book' }, { obsidianLinked: true });
    answerWith(api, (answer) => ({ ...answer, reading: { ...answer.reading, newChapters: 0, newCharacters: 0 } }));
    await showPreview(api, saga);
    await card('Застава');
    // The step says nothing of the reading; the part of the export under it is of the map, not of this load.
    expect(within(screen.getByRole('region', { name: words.previewTitle })).queryByText(/помощник/iu)).toBeNull();
    expect(reasonOf(loadButton())).toBe(words.notUpdated('Застава'));
    cleanup();

    const fresh = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    await showPreview(fresh.api, fresh.saga);
    expect(await screen.findByText(`Будет загружено: 1 книга; записи — 2 новых`)).toBeTruthy();
    expect(reasonOf(loadButton())).toBeNull();
  });

  it('says a live load of books two ways: books then are not loaded, the records are', async () => {
    const { api, saga } = await chosen({ 'Книга 1': 'book', 'Книга 3': 'book', Места: 'materials:place' });
    await api.createBook(saga, { title: 'Застава' });
    await api.createImportQueue(saga, { items: [{ clientItemId: 'one', position: 1, sourceFileName: 'Том 2.md' }] }, { idempotencyKey: 'idem-live-queue' });
    await showPreview(api, saga);
    expect(await screen.findByText(words.queueRunning)).toBeTruthy();
    expect(screen.getByText('Будет загружено: записи — 2 новых')).toBeTruthy();
    // «Связать с «…»» of a book like the folder waits for the load too, as in the window of the link (Sm5 of the review).
    expect(reasonOf(within(await card('Застава')).getByRole('button', { name: words.linkSimilar('Застава') }))).toBe(words.waitLoad);
    cleanup();

    // A volume waits for the decision of the author: the books are said to wait for it.
    const waiting = await chosen({ 'Книга 3': 'book', Места: 'materials:place' });
    const real = waiting.api.listImportQueues.bind(waiting.api);
    vi.spyOn(waiting.api, 'listImportQueues').mockImplementation(async (projectId) => {
      const queues = await real(projectId);
      const queue = (await waiting.api.createImportQueue(projectId, { items: [{ clientItemId: 'wait', position: 1, sourceFileName: 'Застава.md' }] }, { idempotencyKey: 'idem-waiting-queue' }));
      return { ...queues, data: [{ ...queue, status: 'running' as const, items: queue.items.map((item) => ({ ...item, preflightStatus: 'ready' as const, processingStatus: 'failed' as const })) }] };
    });
    await showPreview(waiting.api, waiting.saga);
    expect(await screen.findByText(words.queueWaits('Застава'))).toBeTruthy();
  });
});

// OW3.7: every refusal of the preview with its words and its way out.
describe('the refusals of «что получится»', () => {
  const refuse = (code: string, status: number, fields: Record<string, unknown> = {}, retryAfter?: number) => new MockApiError(status, code, 'previewObsidianImport', code, {
    fields, ...(retryAfter === undefined ? {} : { retryAfter }),
  });
  it.each([
    [refuse('obsidian.folders_overlap', 422, { folders: ['Книга 5', 'Книга 5/Часть 1'] }), words.overlap('Книга 5', 'Книга 5/Часть 1'), []],
    [refuse('obsidian.too_large', 422, { notes: 24_000, bytes: 9_000_000, limitNotes: 20_000, limitBytes: 209_715_200 }), `Выбрано слишком много: ${(24_000).toLocaleString('ru-RU')} заметок при пределе ${(20_000).toLocaleString('ru-RU')}. Уберите часть папок.`, []],
    [refuse('obsidian.too_large', 422, { notes: 900, bytes: 241_172_480, limitNotes: 20_000, limitBytes: 209_715_200 }), 'Выбрано слишком много: 230 МБ при пределе 200 МБ. Уберите часть папок.', []],
    [refuse('obsidian.too_large', 422, { notes: 900, bytes: 9_000, limitNotes: 20_000, limitBytes: 209_715_200, entries: 200_001, limitEntries: 200_000 }), `В выбранных папках слишком много файлов: ${(200_001).toLocaleString('ru-RU')} при пределе ${(200_000).toLocaleString('ru-RU')}. Уберите часть папок.`, []],
    [refuse('obsidian.too_large', 422, { records: 4_200, sagaRecords: 5_400, limitRecords: 5_000 }), `Записей получится ${(5_400).toLocaleString('ru-RU')} при пределе ${(5_000).toLocaleString('ru-RU')} на сагу. Уберите часть папок записей.`, []],
    [refuse('request.validation', 400), words.gone, [words.rescan]],
    [refuse('obsidian.slow', 503), words.slow, [words.retry]],
    [refuse('obsidian.vault_missing', 409, { vaultReason: 'unreadable' }), words.unreadableNow, [words.retry, words.change]],
    [refuse('rate_limit.exceeded', 429), words.rate, []],
    [refuse('obsidian.busy', 429, {}, 0.01), words.busyEnd, [words.retry]],
  ] as const)('says %s', { timeout: 15_000 }, async (error, said, exits) => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    vi.spyOn(api, 'previewObsidianImport').mockRejectedValue(error);
    await showPreview(api, saga);
    // The text of the page is read with its spaces made plain (the thin spaces of the numbers too).
    const line = await screen.findByText(said.replace(/\s/gu, ' '), undefined, { timeout: 10_000 });
    const block = line.closest('[role="alert"]') as HTMLElement;
    for (const exit of exits) expect(within(block).getByRole('button', { name: exit })).toBeTruthy();
    expect(screen.getByRole('button', { name: words.changeChoice })).toBeTruthy();
  });

  it('reads the folder of the saga again on `not_linked`, and says a failure by its block', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    vi.spyOn(api, 'previewObsidianImport').mockImplementation(async () => {
      await api.unlinkObsidianVault(saga);
      throw new MockApiError(409, 'not linked', 'previewObsidianImport', 'obsidian.not_linked');
    });
    renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    expect(await screen.findByText(words.startText, undefined, { timeout: 10_000 })).toBeTruthy();
    cleanup();

    const failed = await chosen({ 'Книга 3': 'book' });
    vi.spyOn(failed.api, 'previewObsidianImport').mockRejectedValue(new MockApiError(500, 'failure', 'previewObsidianImport', 'server.error'));
    await showPreview(failed.api, failed.saga);
    expect(await screen.findByText(words.previewFailed)).toBeTruthy();
  });
});

// OW7.4: the focus stands on the heading of a step when the step changes; the lines of a work are statuses.
describe('the steps of the page for the keyboard and a reader of the screen', () => {
  it('puts the focus on the heading of the step it comes to', async () => {
    const { api, saga } = await chosen({ 'Книга 3': 'book' });
    renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    const preview = await screen.findByRole('heading', { name: words.previewTitle }, { timeout: 10_000 });
    await until(() => expect(document.activeElement).toBe(preview));
    fireEvent.click(await screen.findByRole('button', { name: words.changeChoice }));
    const choice = await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') });
    await until(() => expect(document.activeElement).toBe(choice));
  });
});
