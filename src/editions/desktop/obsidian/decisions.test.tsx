// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { LinkWindow, type LinkedBook } from './LinkWindow';
import type { ObsidianTreeFolder, ObsidianVaultBook } from './obsidian-api';
import { axeViolations, renderAt, theApp, until, withoutShell, withShell } from './obsidian.test-helpers';
import { words } from './words';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const folder = (path: string, extra: Partial<ObsidianTreeFolder> = {}): ObsidianTreeFolder => ({
  path, name: path === '.' ? 'Северный предел' : path.slice(path.lastIndexOf('/') + 1), depth: path === '.' ? 0 : path.split('/').length,
  notes: 2, below: 0, skipped: [], ...extra,
});
const vaultBook = (bookId: string, title: string, at: string, presence: ObsidianVaultBook['presence'] = 'present'): ObsidianVaultBook => ({
  bookId, title, folder: at, loadedAt: null, presence, excluded: [], nested: [],
});

// specs/2026-11-15-desk-obsidian-web.md OW2.9: «Нужно ваше решение» — a book whose folder another holds, a folder not in its place.
describe('the decisions the books of the saga wait for', () => {
  it('names the book that holds the folder and takes the link off with the line of the end', async () => {
    const { api, saga } = await theApp({ obsidianLinked: true });
    await api.beginObsidianVaultChoice(saga);
    withShell();
    const unlinked = vi.spyOn(api, 'unlinkBookFromObsidianFolder');
    renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    const block = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    expect(within(block).getByText(words.relink('Застава (прежняя)', 'Книга 1', 'Застава'))).toBeTruthy();
    fireEvent.click(within(block).getByRole('button', { name: words.unlinkBook }));
    expect(await screen.findByText(words.unlinkedBook('Застава (прежняя)'))).toBeTruthy();
    expect(unlinked).toHaveBeenCalledWith(saga, '5a7b9c1d-2e4f-4a6b-8c0d-1e2f3a4b5c6d');
  });

  it('says a failure of «Снять связь» by «Не получилось — попробуйте ещё раз»', async () => {
    const { api, saga } = await theApp({ obsidianLinked: true });
    await api.beginObsidianVaultChoice(saga);
    withShell();
    vi.spyOn(api, 'unlinkBookFromObsidianFolder').mockRejectedValue(new MockApiError(500, 'failure', 'unlinkBookFromObsidianFolder', 'server.error'));
    renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    const block = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    fireEvent.click(within(block).getByRole('button', { name: words.unlinkBook }));
    expect(await screen.findByText(words.tryAgain)).toBeTruthy();
  });

  it('asks about a book whose folder is not in its place and opens the window of the link for it', async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    withShell();
    const real = api.getObsidianVault.bind(api);
    vi.spyOn(api, 'getObsidianVault').mockImplementation(async (projectId) => ({
      ...(await real(projectId)), books: [vaultBook('book-lost', 'Туманный берег', 'Книги/Туманный берег', 'missing')],
    }));
    const { container } = renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    const block = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    expect(within(block).getByText(words.bookFolderMissing('Туманный берег'))).toBeTruthy();
    fireEvent.click(within(block).getByRole('button', { name: words.linkOther }));
    const window = await screen.findByRole('dialog', { name: words.linkTitle });
    expect(within(window).getByText(words.linkMissing)).toBeTruthy();
    expect(await axeViolations(container.ownerDocument.body)).toEqual([]);
  });
});

function renderWindow(book: LinkedBook, options: { books?: ObsidianVaultBook[]; folders?: ObsidianTreeFolder[]; queueLive?: boolean; link?: () => Promise<void> } = {}) {
  const link = vi.fn(options.link ?? (async () => undefined));
  const onClose = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient()}><ToastsProvider>
      <LinkWindow api={{ linkBookToObsidianFolder: link }} book={book} books={options.books ?? []} onClose={onClose} projectId="saga-1"
        folders={options.folders ?? [folder('.'), folder('Книги'), folder('Книги/Книга 1'), folder('Книги/Книга 1/Часть 1'), folder('Книги/Книга 2'), folder('Черновики')]}
        queueLive={options.queueLive ?? false} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { link, onClose };
}

const radio = (name: string) => screen.getAllByRole('radio').find((one) => (one as HTMLInputElement).value === name)!;

// OW2.10: «Связать с папкой Obsidian» — the book and its folder now, the folders on one choice with the ones the server would not
// take closed and naming the book, «Связать» waiting for a load of books, every refusal in words.
describe('the window «Связать с папкой Obsidian»', () => {
  it('says the folder of the book, a folder not in its place, or why to link a book without one', () => {
    renderWindow({ bookId: 'b1', title: 'Застава', folder: 'Книги/Книга 1' });
    expect(screen.getByText(words.linkedTo('Книги/Книга 1'))).toBeTruthy();
    expect((radio('Книги/Книга 1') as HTMLInputElement).checked).toBe(true);
    cleanup();
    renderWindow({ bookId: 'b1', title: 'Застава', folder: 'Книги/Книга 1', presence: 'missing' });
    expect(screen.getByText(words.linkMissing)).toBeTruthy();
    cleanup();
    renderWindow({ bookId: 'b1', title: 'Застава' });
    expect(screen.getByText(words.linkWhy)).toBeTruthy();
  });

  it('closes the folder of a book of the saga, a folder inside it and one around it — naming the book; a book of the trash gives its folder', () => {
    renderWindow({ bookId: 'b9', title: 'Последний свет' }, {
      books: [vaultBook('b1', 'Туманный берег', 'Книги/Книга 1')],
      folders: [folder('.'), folder('Книги'), folder('Книги/Книга 1'), folder('Книги/Книга 1/Часть 1'), folder('Книги/Книга 2', { book: { bookId: 'b2', title: 'Чёрный маяк' } }), folder('Черновики')],
    });
    expect((radio('Книги/Книга 1') as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText(words.heldBook('Туманный берег'))).toBeTruthy();
    expect((radio('Книги/Книга 1/Часть 1') as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText(words.insideBook('Туманный берег'))).toBeTruthy();
    expect((radio('Книги') as HTMLInputElement).disabled).toBe(true);
    expect(screen.getAllByText(words.holdsBook('Туманный берег')).length).toBeGreaterThan(0);
    expect((radio('Книги/Книга 2') as HTMLInputElement).disabled).toBe(false);
    expect((radio('Черновики') as HTMLInputElement).disabled).toBe(false);
  });

  it('never opens with a folder it would not take chosen: «Связать» says why until a folder it takes is chosen', async () => {
    // The folder of the book is held by another book (a book to link anew): not chosen beforehand.
    renderWindow({ bookId: 'b-old', title: 'Застава (прежняя)', folder: 'Книги/Книга 1' }, { books: [vaultBook('b1', 'Застава', 'Книги/Книга 1')] });
    expect(screen.getAllByRole('radio').filter((one) => (one as HTMLInputElement).checked)).toEqual([]);
    const button = screen.getByRole('button', { name: words.doLink });
    expect(reasonOf(button)).toBe(words.pickFolder);
    fireEvent.click(radio('Черновики'));
    expect(reasonOf(screen.getByRole('button', { name: words.doLink }))).toBeNull();
    cleanup();
    // A folder not in its place is not chosen either: nothing is chosen, and «Связать» says so.
    renderWindow({ bookId: 'b1', title: 'Застава', folder: 'Книги/Пропала', presence: 'missing' });
    expect(reasonOf(screen.getByRole('button', { name: words.doLink }))).toBe(words.pickFolder);
    cleanup();
    // On the page: the window of the block of the decisions for the book whose folder another holds.
    const { api, saga } = await theApp({ obsidianLinked: true });
    await api.beginObsidianVaultChoice(saga);
    withShell();
    const linked = vi.spyOn(api, 'linkBookToObsidianFolder');
    renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    const block = await screen.findByRole('region', { name: words.decisionTitle }, { timeout: 10_000 });
    fireEvent.click(within(block).getByRole('button', { name: words.linkOther }));
    const dialog = await screen.findByRole('dialog', { name: words.linkTitle });
    expect(within(dialog).getAllByRole('radio').filter((one) => (one as HTMLInputElement).checked)).toEqual([]);
    fireEvent.click(within(dialog).getByRole('button', { name: words.doLink }));
    expect(linked).not.toHaveBeenCalled();
  });

  it('links with the line of the end, and waits while books load', async () => {
    const { link, onClose } = renderWindow({ bookId: 'b1', title: 'Последний свет' });
    fireEvent.click(radio('Черновики'));
    fireEvent.click(screen.getByRole('button', { name: words.doLink }));
    expect(await screen.findByText(words.linkedDone('Последний свет', 'Черновики'))).toBeTruthy();
    expect(link).toHaveBeenCalledWith('saga-1', 'b1', { folder: 'Черновики' });
    expect(onClose).toHaveBeenCalled();
    cleanup();
    renderWindow({ bookId: 'b1', title: 'Последний свет', folder: 'Черновики' }, { queueLive: true });
    expect(reasonOf(screen.getByRole('button', { name: words.doLink }))).toBe(words.waitLoad);
  });

  it.each([
    [new MockApiError(409, 'held', 'linkBookToObsidianFolder', 'obsidian.folder_linked', { fields: { folders: ['Черновики'], bookId: 'b1' } }), words.folderLinked('Туманный берег')],
    [new MockApiError(409, 'missing', 'linkBookToObsidianFolder', 'obsidian.folder_missing', { fields: { folders: ['Черновики'] } }), words.folderMissing],
    [new MockApiError(404, 'gone', 'linkBookToObsidianFolder', 'not_found'), words.bookGone],
    [new MockApiError(422, 'overlap', 'linkBookToObsidianFolder', 'obsidian.folders_overlap', { fields: { folders: ['Книги', 'Черновики'] } }), words.overlap('Книги', 'Черновики')],
    [new MockApiError(409, 'busy', 'linkBookToObsidianFolder', 'obsidian.queue_busy', { fields: { queue: { id: 'q1', status: 'running' } } }), words.waitLoad],
  ])('says the refusal %#', async (error, said) => {
    renderWindow({ bookId: 'b9', title: 'Последний свет' }, { books: [vaultBook('b1', 'Туманный берег', 'Книги/Книга 1')], link: async () => { throw error; } });
    fireEvent.click(radio('Черновики'));
    fireEvent.click(screen.getByRole('button', { name: words.doLink }));
    await until(() => expect(screen.getByRole('alert').textContent).toBe(said));
  });
});
