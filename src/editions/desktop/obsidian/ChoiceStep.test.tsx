// SPDX-License-Identifier: AGPL-3.0-only
import { focusManager } from '@tanstack/react-query';
import { act, cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import type { FolderChoice } from './choice';
import { keepChoice, readChoice } from './choice-memory';
import { axeViolations, renderAt, theApp, until, withoutShell, withShell } from './obsidian.test-helpers';
import { notesLine, skippedTitle, skipWords, words } from './words';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function linkedApp(scenario: Parameters<typeof theApp>[0] = {}) {
  const app = await theApp(scenario);
  await app.api.beginObsidianVaultChoice(app.saga);
  withShell();
  return app;
}

const page = (saga: string, step = '') => `/manuscript/obsidian?projectId=${saga}${step}`;

/** The author chose these folders and nothing else of the example. */
function keepChoiceOf(saga: string, kinds: Record<string, FolderChoice>) {
  const nothingElse = ['.', 'Книга 1', 'Книга 2', 'Книга 3', 'Книга 4', 'Книга 5', 'Книга 5/Герои', 'Персонажи', 'Места', 'Шаблоны'];
  keepChoice('user-local', saga, { kinds: { ...Object.fromEntries(nothingElse.map((path) => [path, 'skip' as const])), ...kinds }, notes: [] });
}
const heading = () => screen.findByRole('heading', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
const choiceOf = (name: string) => screen.getByRole('button', { name: new RegExp(`^${words.choiceOf(name)}`, 'u') });
const busy = (seconds: number) => new MockApiError(429, 'another operation reads a vault', 'scanObsidianVault', 'obsidian.busy', { retryAfter: seconds });

async function pick(name: string, label: string) {
  fireEvent.click(choiceOf(name));
  fireEvent.click(await screen.findByRole('option', { name: new RegExp(`^${label}`, 'u') }));
}

// specs/2026-11-15-desk-obsidian-web.md OW2.1–OW2.7: the folders of the look with their choices, the defaults, the memory of the
// choice and the way to «что получится».
describe('the choice of the folders', () => {
  it('lists the folders in lists with their notes and the words of their books, nested ones folded with their own choices said', async () => {
    const { api, saga } = await linkedApp({ obsidianLinked: true });
    const { container } = renderAt(api, page(saga));
    await heading();
    const list = await screen.findByRole('list', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 });
    expect(within(list).getAllByRole('list').length).toBeGreaterThan(0);
    expect(list.getAttribute('role')).not.toBe('tree');
    expect(within(list).getByText(notesLine(0, 30))).toBeTruthy();
    expect(within(list).getByText(words.bookIn('Застава'))).toBeTruthy();
    expect(within(list).getByText(words.bookTrash('Белый порт'))).toBeTruthy();
    const folded = screen.getByRole('button', { name: words.nested('Часть 2', 1) });
    expect(folded.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('button', { name: new RegExp(`^${words.choiceOf('Черновики')}`, 'u') })).toBeNull();
    fireEvent.click(folded);
    expect(choiceOf('Черновики')).toBeTruthy();
    expect(await axeViolations(container)).toEqual([]);
  });

  it('takes the defaults: the hints of books and records, parts with their book, and asks the preview without an exclusion', async () => {
    const { api, saga } = await linkedApp();
    const preview = vi.spyOn(api, 'previewObsidianImport');
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Книга 5').textContent).toContain(words.book));
    expect(choiceOf('Герои').textContent).toContain('Записи о героях');
    expect(choiceOf('Часть 1').textContent).toContain(words.together('Книга 5'));
    expect(choiceOf('Шаблоны').textContent).toContain(words.skip);
    expect(screen.getByText('Выбрано: 3 книги, 3 папки записей')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: words.show }));
    await until(() => expect(preview).toHaveBeenCalled());
    expect(preview.mock.calls[0]![1]).toEqual({
      books: ['Книга 1', 'Книга 4', 'Книга 5'],
      materials: [{ path: 'Персонажи', kind: 'character' }, { path: 'Места', kind: 'place' }, { path: 'Книга 5/Герои', kind: 'character' }],
    });
  });

  it('closes «Книга» inside a chosen book and around one with the reason by the variant', async () => {
    const { api, saga } = await linkedApp();
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Часть 1').textContent).toContain(words.together('Книга 5')));
    fireEvent.click(choiceOf('Часть 1'));
    const book = await screen.findByRole('option', { name: new RegExp(`^${words.book}`, 'u') });
    expect(book.getAttribute('aria-disabled')).toBe('true');
    expect(book.textContent).toContain(words.whyInside('Книга 5'));
    fireEvent.keyDown(book, { key: 'Escape' });
    await pick('Часть 1', 'Записи о местах');
    expect(choiceOf('Часть 1').textContent).toContain('Записи о местах');
  });

  it('remembers the choice of the author past the page by relative paths, without the path of the folder', async () => {
    const { api, saga } = await linkedApp();
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Книга 3')).toBeTruthy());
    await pick('Книга 3', words.book);
    expect(readChoice('user-local', saga).kinds).toEqual({ 'Книга 3': 'book' });
    cleanup();
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Книга 3').textContent).toContain(words.book));
    expect(Object.values({ ...window.localStorage }).some((value) => String(value).includes('/home/author'))).toBe(false);
  });

  it('names the choice of a folder by its path when another folder has its name', async () => {
    const { api, saga } = await linkedApp();
    const folder = (path: string, depth: number) => ({ path, name: path.slice(path.lastIndexOf('/') + 1), depth, notes: 2, below: 0, skipped: [] });
    vi.spyOn(api, 'scanObsidianVault').mockResolvedValue({
      folders: [{ ...folder('.', 0), name: 'Северный предел' }, folder('Книга 1', 1), folder('Книга 1/Часть 1', 2), folder('Книга 2', 1), folder('Книга 2/Часть 1', 2)],
      truncated: false, entries: 8, skipped: [],
    });
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(screen.getByRole('button', { name: new RegExp(`^${words.choiceOf('Книга 1/Часть 1')}`, 'u') })).toBeTruthy());
    expect(screen.getByRole('button', { name: new RegExp(`^${words.choiceOf('Книга 2/Часть 1')}`, 'u') })).toBeTruthy();
    expect(choiceOf('Книга 1')).toBeTruthy();
  });

  it('forgets the folders the look no longer has only by a whole look: not by a cut one, nor one with notes only in the cloud', async () => {
    for (const [scenario, cloud] of [[{ obsidianVault: 'truncated' as const }, false], [{}, true]] as const) {
      const { api, saga } = await linkedApp(scenario);
      keepChoice('user-local', saga, { kinds: { 'Книга 3': 'book', 'В облаке': 'materials:place' }, notes: ['В облаке/Порт.md'] });
      if (cloud) {
        const real = api.scanObsidianVault.bind(api);
        vi.spyOn(api, 'scanObsidianVault').mockImplementation(async (projectId) => {
          const tree = await real(projectId);
          return { ...tree, skipped: [...tree.skipped.filter((skip) => skip.reason !== 'cloud_only'), { reason: 'cloud_only', count: 4 }] };
        });
      }
      renderAt(api, page(saga));
      await heading();
      await until(() => expect(choiceOf('Книга 3').textContent).toContain(words.book));
      expect(readChoice('user-local', saga)).toEqual({ kinds: { 'Книга 3': 'book', 'В облаке': 'materials:place' }, notes: ['В облаке/Порт.md'] });
      cleanup();
    }
    // A whole look: the folder it has not and its notes drop out.
    const { api, saga } = await linkedApp();
    vi.spyOn(api, 'scanObsidianVault').mockImplementation(async () => ({
      folders: [{ path: '.', name: 'Северный предел', depth: 0, notes: 0, below: 2, skipped: [] }, { path: 'Книга 3', name: 'Книга 3', depth: 1, notes: 2, below: 0, skipped: [] }],
      truncated: false, entries: 3, skipped: [],
    }));
    keepChoice('user-local', saga, { kinds: { 'Книга 3': 'book', 'В облаке': 'materials:place' }, notes: ['В облаке/Порт.md'] });
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(readChoice('user-local', saga)).toEqual({ kinds: { 'Книга 3': 'book' }, notes: [] }));
  });

  it('says why «Показать, что получится» waits: nothing chosen, the preview of this page still read', async () => {
    const { api, saga } = await linkedApp();
    renderAt(api, page(saga));
    await heading();
    for (const name of ['Книга 1', 'Книга 4', 'Книга 5', 'Герои', 'Персонажи', 'Места']) {
      await until(() => expect(choiceOf(name)).toBeTruthy());
      await pick(name, words.skip);
    }
    expect(reasonOf(screen.getByRole('button', { name: words.show }))).toBe(words.whyNone);
    await pick('Книга 3', words.book);
    vi.spyOn(api, 'previewObsidianImport').mockImplementation(() => new Promise(() => undefined));
    fireEvent.click(screen.getByRole('button', { name: words.show }));
    fireEvent.click(await screen.findByRole('button', { name: words.changeChoice }));
    await heading();
    expect(reasonOf(screen.getByRole('button', { name: words.show }))).toBe(words.whyReading);
  });
});

// OW2.8, OW7.1: the look over the folder is read on the entry and by the button, never by itself again; it says what goes and why
// it did not pass.
describe('the look over the folder', () => {
  it('is not read again on the return to the window or to the step; «Прочитать папку заново» reads it', async () => {
    const { api, saga } = await linkedApp();
    const scans = vi.spyOn(api, 'scanObsidianVault');
    const { go } = renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Книга 1')).toBeTruthy());
    expect(scans).toHaveBeenCalledTimes(1);
    act(() => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
    });
    act(() => go(page(saga, '&step=preview')));
    act(() => go(page(saga)));
    await heading();
    expect(scans).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: words.rescan }));
    await until(() => expect(scans).toHaveBeenCalledTimes(2));
    expect(scans.mock.calls.every((call) => call.length === 1)).toBe(true);
  });

  it('is old after «Связать» and «Снять связь» and after any write of the bridge: read on the next entry to the step, never by itself', async () => {
    // «Снять связь» of the block of the decisions on the step: no reading now, one on the next entry.
    const { api, saga } = await linkedApp({ obsidianLinked: true });
    const scans = vi.spyOn(api, 'scanObsidianVault');
    const { go } = renderAt(api, page(saga));
    const block = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    expect(scans).toHaveBeenCalledTimes(1);
    fireEvent.click(within(block).getByRole('button', { name: words.unlinkBook }));
    expect(await screen.findByText(words.unlinkedBook('Застава (прежняя)'))).toBeTruthy();
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(scans).toHaveBeenCalledTimes(1);
    act(() => go(page(saga, '&step=preview')));
    act(() => go(page(saga)));
    await until(() => expect(scans).toHaveBeenCalledTimes(2));
    cleanup();

    // «Связать с «…»» of a card of «что получится»: the next entry to the step reads the folder again.
    const similar = await linkedApp();
    keepChoiceOf(similar.saga, { 'Книга 1': 'book' });
    await similar.api.createBook(similar.saga, { title: 'Застава' });
    const linkedScans = vi.spyOn(similar.api, 'scanObsidianVault');
    renderAt(similar.api, page(similar.saga));
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    fireEvent.click(await screen.findByRole('button', { name: words.linkSimilar('Застава') }, { timeout: 10_000 }));
    expect(await screen.findByText(words.linkedDone('Застава', 'Книга 1'))).toBeTruthy();
    expect(linkedScans).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: words.changeChoice }));
    await until(() => expect(linkedScans).toHaveBeenCalledTimes(2));
    cleanup();

    // A load of records alone: the next entry to the step reads the folder again.
    const records = await linkedApp();
    keepChoiceOf(records.saga, { Места: 'materials:place' });
    const recordScans = vi.spyOn(records.api, 'scanObsidianVault');
    const view = renderAt(records.api, page(records.saga));
    fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 10_000 }));
    fireEvent.click(await screen.findByRole('button', { name: words.load }, { timeout: 10_000 }));
    expect(await screen.findByText(/^Создано 2/u, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(recordScans).toHaveBeenCalledTimes(1);
    act(() => view.go(page(records.saga)));
    await until(() => expect(recordScans).toHaveBeenCalledTimes(2));
  }, 30_000);

  it('says why «Прочитать папку заново» waits while the folder is read', async () => {
    const { api, saga } = await linkedApp();
    renderAt(api, page(saga));
    await heading();
    await until(() => expect(choiceOf('Книга 1')).toBeTruthy());
    vi.spyOn(api, 'scanObsidianVault').mockImplementation(() => new Promise(() => undefined));
    fireEvent.click(screen.getByRole('button', { name: words.rescan }));
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.rescan }))).toBe(words.scanning));
  });

  it('says it reads, and after five seconds how long it may take; a request the page left goes on into the cache', async () => {
    const { api, saga } = await linkedApp();
    let answer: () => void = () => undefined;
    const real = api.scanObsidianVault.bind(api);
    const scans = vi.spyOn(api, 'scanObsidianVault').mockImplementation((projectId) => new Promise((resolve) => { answer = () => resolve(real(projectId)); }));
    const view = renderAt(api, page(saga));
    // The line of the step is a status of its own (the part of the export under the step has its own).
    expect((await screen.findByText(words.scanning, undefined, { timeout: 10_000 })).closest('[role="status"]')).toBeTruthy();
    await until(() => expect(screen.getByText(words.scanLong)).toBeTruthy());
    view.unmount();
    answer();
    renderAt(api, page(saga), { client: view.client });
    await until(() => expect(choiceOf('Книга 1')).toBeTruthy());
    expect(scans).toHaveBeenCalledTimes(1);
  }, 15_000);

  it('says a tree cut by its limits, a folder without notes, notes only in the cloud, and every reason of what was skipped', async () => {
    const cut = await linkedApp({ obsidianVault: 'truncated' });
    renderAt(cut.api, page(cut.saga));
    expect(await screen.findByText(words.cut, undefined, { timeout: 10_000 })).toBeTruthy();
    cleanup();

    for (const [cloud, said] of [[false, words.noNotes], [true, words.cloudEmpty]] as const) {
      const empty = await linkedApp();
      vi.spyOn(empty.api, 'scanObsidianVault').mockResolvedValue({ folders: [], truncated: false, entries: 3, skipped: cloud ? [{ reason: 'cloud_only', count: 3 }] : [] });
      renderAt(empty.api, page(empty.saga));
      expect(await screen.findByText(said, undefined, { timeout: 10_000 })).toBeTruthy();
      cleanup();
    }

    const { api, saga } = await linkedApp();
    const every = Object.keys(skipWords).map((reason, index) => ({ reason: reason as keyof typeof skipWords, count: index + 1 }));
    const real = await api.scanObsidianVault(saga);
    vi.spyOn(api, 'scanObsidianVault').mockResolvedValue({ ...real, skipped: every });
    renderAt(api, page(saga));
    expect(await screen.findByText(words.cloudNote, undefined, { timeout: 10_000 })).toBeTruthy();
    fireEvent.click(screen.getByText(skippedTitle(every.reduce((sum, skip) => sum + skip.count, 0), 'files')));
    for (const skip of every) expect(screen.getByText(`${skipWords[skip.reason]} — ${skip.count}`)).toBeTruthy();
  });

  it('waits for a vault read by another operation by its Retry-After, saying it, up to six times', async () => {
    const { api, saga } = await linkedApp();
    const real = api.scanObsidianVault.bind(api);
    let first = true;
    vi.spyOn(api, 'scanObsidianVault').mockImplementation((projectId) => {
      if (first) {
        first = false;
        return Promise.reject(busy(1));
      }
      return real(projectId);
    });
    renderAt(api, page(saga));
    expect(await screen.findByText(words.busy(1), undefined, { timeout: 10_000 })).toBeTruthy();
    await until(() => expect(choiceOf('Книга 1')).toBeTruthy());
    cleanup();

    const always = await linkedApp();
    const scans = vi.spyOn(always.api, 'scanObsidianVault').mockRejectedValue(busy(0.01));
    renderAt(always.api, page(always.saga));
    expect(await screen.findByText(words.busyEnd, undefined, { timeout: 10_000 })).toBeTruthy();
    // Six tries in all — the first and five repeats: «после шести попыток» of Data (Sm9 of the review).
    expect(scans).toHaveBeenCalledTimes(6);
    expect(screen.getByRole('button', { name: words.retry })).toBeTruthy();
  }, 15_000);

  it.each([
    ['slow', words.slow, [words.retry]],
    [{ missing: 'not_found' }, words.missing, [words.checkAgain, words.change]],
    [{ missing: 'not_folder' }, words.notFolderNow, [words.change]],
    [{ missing: 'unreadable' }, words.unreadableNow, [words.retry, words.change]],
  ] as const)('says the refusal %j with its way out', async (vault, said, exits) => {
    const { api, saga } = await linkedApp();
    // The folder of the saga answered before; the look meets the refusal.
    const real = api.getObsidianVault.bind(api);
    vi.spyOn(api, 'getObsidianVault').mockImplementation(async (projectId) => ({ ...(await real(projectId)), availability: 'available' }));
    api.setScenario({ ...api.getScenario(), obsidianVault: vault });
    renderAt(api, page(saga));
    const alert = await screen.findByText(said, undefined, { timeout: 10_000 });
    const block = alert.closest('[role="alert"]') as HTMLElement;
    for (const exit of exits) expect(within(block).getByRole('button', { name: exit })).toBeTruthy();
  });

  it('reads the folder of the saga again on `not_linked`, says the limit of requests and a failure by its block', async () => {
    const { api, saga } = await linkedApp();
    // The folder unlinked while the look went: the look is refused `not_linked` — the page reads the folder again, whenever.
    vi.spyOn(api, 'scanObsidianVault').mockImplementationOnce(async () => {
      await api.unlinkObsidianVault(saga);
      throw new MockApiError(409, 'not linked', 'scanObsidianVault', 'obsidian.not_linked');
    });
    renderAt(api, page(saga));
    expect(await screen.findByText(words.startText, undefined, { timeout: 10_000 })).toBeTruthy();
    cleanup();

    for (const [error, said] of [
      [new MockApiError(429, 'too often', 'scanObsidianVault', 'rate_limit.exceeded'), words.rate],
      [new MockApiError(500, 'failure', 'scanObsidianVault', 'server.error'), words.scanFailed],
    ] as const) {
      const other = await linkedApp();
      vi.spyOn(other.api, 'scanObsidianVault').mockRejectedValue(error);
      renderAt(other.api, page(other.saga));
      expect(await screen.findByText(said, undefined, { timeout: 10_000 })).toBeTruthy();
      cleanup();
    }
  });
});
