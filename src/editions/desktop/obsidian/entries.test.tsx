// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { openPages } from '../../../app/pages';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { renderAt, theApp, withoutShell, type TheApp } from './obsidian.test-helpers';
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

const bridgeOperations = [
  'getObsidianVault', 'beginObsidianVaultChoice', 'unlinkObsidianVault', 'scanObsidianVault', 'previewObsidianImport',
  'importObsidianMaterials', 'importObsidianBooks', 'linkBookToObsidianFolder', 'unlinkBookFromObsidianFolder',
] as const;

function spyBridge(api: TheApp) {
  return bridgeOperations.map((operation) => vi.spyOn(api, operation));
}

async function withBook(api: TheApp, saga: string) {
  await api.createBook(saga, { title: 'Застава' });
}

// specs/2026-11-15-desk-obsidian-web.md OW1.1, OW1.2: the entries of the bridge — «Из Obsidian…» of «Книги» and of «Ваши
// материалы» — only for an author who may change the saga, only in the app; each carries where it came from.
describe('the entries of the bridge with Obsidian', () => {
  it('stands in the head of «Книги» beside «Добавить книги», open while books load, and leads with the way back to the books', async () => {
    const { api, saga } = await theApp();
    await withBook(api, saga);
    const { steps } = renderAt(api, `/manuscript/books?projectId=${saga}`);
    const entry = await screen.findByRole('button', { name: words.from }, { timeout: 10_000 });
    const head = entry.parentElement!;
    expect(within(head).getAllByRole('button').map((button) => button.textContent).slice(0, 2)).toEqual(['Добавить книги', words.from]);
    expect(reasonOf(entry)).toBeNull();
    fireEvent.click(entry);
    expect(steps.at(-1)).toEqual({ path: '/manuscript/obsidian', query: { projectId: saga }, options: { from: { section: 'books' } } });
  });

  it('stands in the first steps of a saga without a book with its caption', async () => {
    const { api, saga } = await theApp();
    renderAt(api, `/manuscript/books?projectId=${saga}`);
    expect(await screen.findByText(words.fromCaption, undefined, { timeout: 10_000 })).toBeTruthy();
    const steps = screen.getByText(words.fromCaption).parentElement!;
    expect(within(steps).getAllByRole('button').map((button) => button.textContent)).toEqual(['Добавить книги', words.from]);
    // «Из Obsidian…» stands exactly where «Добавить книги» stands on the screen: as many of the one as of the other (m9).
    expect(screen.getAllByRole('button', { name: words.from }).length).toBe(screen.getAllByRole('button', { name: 'Добавить книги' }).length);
  });

  it('stays open while a load of books goes', async () => {
    const { api, saga } = await theApp();
    await withBook(api, saga);
    await api.createImportQueue(saga, { items: [{ clientItemId: 'one', position: 1, sourceFileName: 'Том 2.md' }] }, { idempotencyKey: 'idem-queue-entry' });
    renderAt(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByText('Файл не дошёл', undefined, { timeout: 10_000 });
    expect(reasonOf(screen.getByRole('button', { name: words.from }))).toBeNull();
    expect(reasonOf(screen.getByRole('button', { name: 'Добавить книги' }))).not.toBeNull();
  });

  it('stands in «Ваши материалы» after «Записать самому», quieter, the words of an empty list as they were', async () => {
    const { api, saga } = await theApp();
    const { steps } = renderAt(api, `/memory?projectId=${saga}&part=materials`, { page: 'memory' });
    const entry = await screen.findByRole('button', { name: words.from }, { timeout: 10_000 });
    const own = screen.getByRole('button', { name: 'Записать самому' });
    expect(own.getAttribute('data-variant')).toBe('primary');
    expect(entry.getAttribute('data-variant')).toBe('outline');
    expect([...entry.parentElement!.children].indexOf(entry)).toBeGreaterThan([...entry.parentElement!.children].indexOf(own));
    expect(await screen.findByText('Добавьте запись сами: навык, место, героя.')).toBeTruthy();
    fireEvent.click(entry);
    expect(steps.at(-1)).toEqual({ path: '/manuscript/obsidian', query: { projectId: saga }, options: { from: { section: 'memory' } } });
  });

  it('is not given to a reader of the saga, who lands on «Книги» from the page of the bridge', { timeout: 20_000 }, async () => {
    const { api, saga } = await theApp();
    await withBook(api, saga);
    api.setScenario({ ...api.getScenario(), actorRole: 'viewer' });
    const calls = spyBridge(api);
    renderAt(api, `/manuscript/books?projectId=${saga}`);
    await screen.findByRole('button', { name: /Действия книги .*Застава/u }, { timeout: 10_000 });
    expect(screen.queryByRole('button', { name: words.from })).toBeNull();
    cleanup();
    const { steps } = renderAt(api, `/manuscript/obsidian?projectId=${saga}`);
    await vi.waitFor(() => expect(steps.at(-1)).toEqual({ path: '/manuscript/books', query: { projectId: saga }, options: { replace: true } }), { timeout: 10_000 });
    expect(calls.every((call) => call.mock.calls.length === 0)).toBe(true);
  });

  it('is not in the open pages without the members of the app, and no operation of the bridge is called', { timeout: 20_000 }, async () => {
    const { api, saga } = await theApp();
    await withBook(api, saga);
    const calls = spyBridge(api);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}><ToastsProvider>
        {openPages.manuscript.render({ api, navigate: vi.fn(), route: createAppRoute('/manuscript/books', { projectId: saga }), user: { id: 'user-local' } as never })}
        {openPages.memory.render({ api, navigate: vi.fn(), route: createAppRoute('/memory', { projectId: saga, part: 'materials' }), user: { id: 'user-local' } as never })}
      </ToastsProvider></QueryClientProvider>,
    );
    await screen.findByRole('button', { name: /Действия книги .*Застава/u }, { timeout: 10_000 });
    await screen.findByRole('button', { name: 'Записать самому' }, { timeout: 10_000 });
    expect(screen.queryByRole('button', { name: words.from })).toBeNull();
    expect(screen.queryByText(words.fromCaption)).toBeNull();
    expect(calls.every((call) => call.mock.calls.length === 0)).toBe(true);
  });

  it('has the way back of the history: «К книгам», «К «Что помнит помощник»», and none opened anew', async () => {
    const { api, saga } = await theApp();
    for (const [from, back] of [['books', 'К книгам'], ['memory', 'К «Что помнит помощник»']] as const) {
      const route = { ...createAppRoute('/manuscript/obsidian', { projectId: saga }), from: { section: from } };
      render(<QueryClientProvider client={new QueryClient()}><InShell api={api} route={route}><p>Страница моста</p></InShell></QueryClientProvider>);
      expect(within(await screen.findByRole('navigation', { name: 'Возврат' })).getByRole('button').textContent).toBe(back);
      cleanup();
    }
    render(<QueryClientProvider client={new QueryClient()}><InShell api={api} route={createAppRoute('/manuscript/obsidian', { projectId: saga })}><p>Страница моста</p></InShell></QueryClientProvider>);
    await screen.findByText('Страница моста');
    expect(screen.queryByRole('navigation', { name: 'Возврат' })).toBeNull();
  });
});
