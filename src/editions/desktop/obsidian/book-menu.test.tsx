// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { openPages } from '../../../app/pages';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import type { ChoiceMemory, FolderChoice } from './choice';
import { keepChoice } from './choice-memory';
import { obsidianKeys } from './obsidian-api';
import { aBookOfItsFolder, axeViolations, menuOf, renderInTheApp, settle, theApp, withoutShell, withShell, type TheApp } from './obsidian.test-helpers';
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
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const obsidianItems = (menu: HTMLElement) => within(menu).queryAllByRole('menuitem').filter((item) => /Obsidian/u.test(item.textContent ?? ''));

/** A book of the saga deleted, another one linked with its folder, then the first one given back: it is to be linked anew. */
async function aBookToLinkAnew(api: TheApp, saga: string) {
  const older = await api.createBook(saga, { title: 'Старая застава' });
  await api.linkBookToObsidianFolder(saga, older.id, { folder: 'Книга 3' });
  const newer = await api.createBook(saga, { title: 'Новая застава' });
  await api.deleteBook(older.id);
  await api.linkBookToObsidianFolder(saga, newer.id, { folder: 'Книга 3' });
  await api.restoreBook(older.id);
  return { older, newer };
}

// specs/2026-11-15-desk-obsidian-book-web.md OU1: one item of the edition in the menu of every live book of a saga with its folder.
describe('the item of the bridge in the menu of a book', () => {
  it('is not in the menu of a book of a saga without a folder of Obsidian', { timeout: 30_000 }, async () => {
    const { api, saga } = await theApp();
    await api.createBook(saga, { title: 'Застава' });
    const vaults = vi.spyOn(api, 'getObsidianVault');
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    // The folder of the saga is read and says it is not chosen.
    await waitFor(() => expect(vaults).toHaveBeenCalledTimes(1));
    expect((await vaults.mock.results[0]!.value).linked).toBe(false);
    const menu = menuOf('Застава');
    expect(within(menu).getByRole('menuitem', { name: 'Новая версия из файла…' })).toBeTruthy();
    expect(obsidianItems(menu)).toEqual([]);
  });

  it('updates a book of a folder and links the others — a book without one and a book to link anew — by the window of the link', { timeout: 40_000 }, async () => {
    const { api, saga } = await aBookOfItsFolder();
    await api.createBook(saga, { title: 'Белый порт' });
    await aBookToLinkAnew(api, saga);
    const vaults = vi.spyOn(api, 'getObsidianVault');
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });

    const ofFolder = await waitFor(() => {
      const items = obsidianItems(menuOf('Застава'));
      expect(items).toHaveLength(1);
      return items[0]!;
    });
    expect(ofFolder.textContent).toBe(updateWords.updateItem);
    // The item stands after «Новая версия из файла…».
    const items = within(screen.getByRole('menu', { name: 'Действия книги' })).getAllByRole('menuitem').map((item) => item.textContent);
    expect(items.indexOf(updateWords.updateItem)).toBe(items.findIndex((item) => item?.startsWith('Новая версия из файла…')) + 1);
    fireEvent.click(within(screen.getByRole('menu')).getByRole('button', { name: 'Закрыть меню', hidden: true }));
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());

    for (const title of ['Белый порт', 'Старая застава']) {
      const [item] = obsidianItems(menuOf(title));
      expect(item!.textContent).toBe(updateWords.linkItem);
      fireEvent.click(item!);
      const link = await screen.findByRole('dialog', { name: words.linkTitle });
      expect(within(link).getByText(title)).toBeTruthy();
      expect(await within(link).findByRole('radio', { name: /Книга 3/u }, { timeout: 15_000 })).toBeTruthy();
      if (title === 'Старая застава') {
        expect(within(link).getByText(words.linkedTo('Книга 3'))).toBeTruthy();
        expect(within(link).getByText(words.heldBook('Новая застава'))).toBeTruthy();
      }
      expect(await axeViolations(link)).toEqual([]);
      fireEvent.click(within(link).getByRole('button', { name: words.cancel }));
      await waitFor(() => expect(screen.queryByRole('dialog', { name: words.linkTitle })).toBeNull());
    }
    // OU1.3: the folder of the saga is read once for the page, whatever menus open.
    expect(vaults).toHaveBeenCalledTimes(1);
  });

  it('waits and says why while the saga loads books', { timeout: 30_000 }, async () => {
    const { api, saga } = await aBookOfItsFolder();
    await api.createImportQueue(saga, { items: [{ clientItemId: 'tom-1', position: 1, sourceFileName: 'Том.fb2' }] }, { idempotencyKey: 'idem-menu-queue-1' });
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    const item = await waitFor(() => {
      const [one] = obsidianItems(menuOf('Застава'));
      expect(one?.getAttribute('aria-disabled')).toBe('true');
      return one!;
    }, { timeout: 10_000 });
    expect(item.textContent).toBe(`${updateWords.updateItem}${words.waitLoad}`);
    fireEvent.click(item);
    expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull();
  });

  it('comes once the folder of the saga is read, and the folder is read again when a load from it ends', { timeout: 40_000 }, async () => {
    const { api, saga } = await theApp();
    await api.beginObsidianVaultChoice(saga);
    const preview = await api.previewObsidianImport(saga, { books: ['Книга 3'] });
    const vaults = vi.spyOn(api, 'getObsidianVault');
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    // A load from the folder started on the page of the bridge: the write of its books in the cache of the mutations.
    const loaded = await api.importObsidianBooks(saga, { books: [{ folder: 'Книга 3', fingerprint: preview.books[0]!.fingerprint }] }, { idempotencyKey: 'idem-menu-load-1' });
    await client.getMutationCache().build(client, { mutationKey: obsidianKeys.loadBooks, mutationFn: async () => loaded })
      .execute({ projectId: saga, previewId: 'preview-1', run: 'run-1', parts: { materials: false, books: true }, body: {}, idempotencyKey: 'idem-menu-load-1' });
    renderInTheApp(api, `/manuscript/books?projectId=${saga}`, { client });
    await waitFor(() => expect(vaults).toHaveBeenCalledTimes(1), { timeout: 10_000 });
    await settle(api, saga);
    await screen.findByRole('article', { name: 'Книга 3' }, { timeout: 15_000 });
    await waitFor(() => expect(vaults.mock.calls.length).toBeGreaterThan(1), { timeout: 15_000 });
    await waitFor(() => expect(obsidianItems(menuOf('Книга 3')).map((item) => item.textContent)).toEqual([updateWords.updateItem]), { timeout: 10_000 });
  });

  it('is not in the menu of the open pages without the member of the app (the site)', { timeout: 30_000 }, async () => {
    const { api, saga } = await aBookOfItsFolder();
    const vaults = vi.spyOn(api, 'getObsidianVault');
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
        {openPages.manuscript.render({ api, navigate: vi.fn(), route: createAppRoute('/manuscript/books', { projectId: saga }), user: { id: 'user-local' } as never })}
      </ToastsProvider></QueryClientProvider>,
    );
    await screen.findByRole('article', { name: 'Застава' }, { timeout: 15_000 });
    const menu = menuOf('Застава');
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual([
      'Читать с начала', 'Новая версия из файла…', 'Как разобралась книга', 'Переименовать', 'Обложка…', 'Скачать книгу…', 'Удалить книгу…',
    ]);
    expect(vaults).not.toHaveBeenCalled();
  });
});

const nothingElse: Record<string, FolderChoice> = {
  'Книга 2': 'skip', 'Книга 3': 'skip', 'Книга 4': 'skip', 'Книга 5': 'skip', 'Книга 5/Герои': 'skip', Персонажи: 'skip', Места: 'skip', Шаблоны: 'skip',
};

/** The page of the bridge at «что получится» of the folder of the book «Застава», its notes edited since the load. */
async function theBridge(prepare: (api: TheApp, saga: string) => Promise<void> = async () => undefined) {
  const app = await aBookOfItsFolder();
  app.api.setScenario({ ...app.api.getScenario(), obsidianEdited: true });
  await prepare(app.api, app.saga);
  withShell();
  const memory: ChoiceMemory = { kinds: { ...nothingElse, 'Книга 1': 'book' }, notes: [] };
  keepChoice('user-local', app.saga, memory);
  renderInTheApp(app.api, `/manuscript/obsidian?projectId=${app.saga}`);
  fireEvent.click(await screen.findByRole('button', { name: words.show }, { timeout: 15_000 }));
  const card = await screen.findByRole('listitem', { name: 'Застава' }, { timeout: 15_000 });
  return { ...app, card };
}

// OU2.8: the card of a book of the saga with changes in its notes opens the same window.
describe('the card «в заметках есть изменения» of the page of the bridge', () => {
  it('opens «Обновить из Obsidian…», and after the update the page says «Книги изменились»', { timeout: 40_000 }, async () => {
    const { card } = await theBridge();
    expect(card.textContent).toContain(words.stChanged('Застава'));
    expect(screen.getByText(words.notUpdated('Застава'))).toBeTruthy();
    fireEvent.click(within(card).getByRole('button', { name: updateWords.updateItem }));
    const window = await screen.findByRole('dialog', { name: updateWords.title });
    await within(window).findByRole('heading', { name: updateWords.planTitle('Застава') }, { timeout: 15_000 });
    fireEvent.click(within(window).getByRole('button', { name: updateWords.confirm }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull(), { timeout: 15_000 });
    expect(await screen.findByText(words.staleBooks, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(reasonOf(screen.getByRole('button', { name: words.load }))).toBe(words.whyStale);
  });

  it('waits with its reason while the saga loads books', { timeout: 40_000 }, async () => {
    const { card } = await theBridge(async (api, saga) => {
      await api.createImportQueue(saga, { items: [{ clientItemId: 'tom-1', position: 1, sourceFileName: 'Том.fb2' }] }, { idempotencyKey: 'idem-bridge-queue-1' });
    });
    const button = within(card).getByRole('button', { name: updateWords.updateItem });
    expect(reasonOf(button)).toBe(words.waitLoad);
    fireEvent.click(button);
    expect(screen.queryByRole('dialog', { name: updateWords.title })).toBeNull();
  });
});
