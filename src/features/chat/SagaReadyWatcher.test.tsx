import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { IndexingStatus } from '../../entities/manuscript/api';
import { manuscriptQueryKeys } from '../../entities/manuscript/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { TopBarStrip } from '../../ui/navigation/TopBarStrip';
import { SagaReadyWatcher } from './SagaReadyWatcher';
import { forgetTellings } from './saga-ready';

const ready = { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } };
const readyAt = '2026-11-09T12:20:00.000Z';

function statusOf(sagaReady?: IndexingStatus['sagaReady']): IndexingStatus {
  return { published: ready, steps: [], ...(sagaReady ? { sagaReady } : {}) };
}

function renderWatcher(path: string, query: Record<string, string>, status: () => IndexingStatus, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const navigate = vi.fn();
  const api = { getIndexingStatus: vi.fn(async () => status()) };
  const page = (nextPath: string, nextQuery: Record<string, string>) => (
    <QueryClientProvider client={client}>
      <SagaReadyWatcher api={api} navigate={navigate} route={createAppRoute(nextPath, nextQuery)}>
        <header>Шапка</header>
        <TopBarStrip />
        <main>Страница</main>
      </SagaReadyWatcher>
    </QueryClientProvider>
  );
  const view = render(page(path, query));
  return { api, client, navigate, view, goTo: (nextPath: string, nextQuery: Record<string, string>) => view.rerender(page(nextPath, nextQuery)) };
}

const strip = () => screen.queryByRole('status', { name: /прочитан[аы] — можно спрашивать/u });

// FS4.2, FS4.3 of the spec «Сага готова» (specs/2026-11-09): the page tells of the readiness the server names, once.
// FS4.2, FS4.3 of the spec «Сага готова» (specs/2026-11-09) and SS3.5 of specs/2026-11-10-ux3-saga-state.md: the page tells
// once of the readiness the server names, in a strip under the top bar in the words of the line of the shelf.
describe('SagaReadyWatcher', () => {
  beforeEach(() => {
    window.localStorage.clear();
    forgetTellings();
  });
  afterEach(cleanup);

  it('tells once, under the top bar, of a saga that became ready and opens its chat', async () => {
    const { api, client, navigate } = renderWatcher('/saga', { projectId: 'project-white-port' },
      () => statusOf({ at: readyAt, books: 2, chapters: 41 }));
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(strip()!.textContent).toContain('Книги прочитаны — можно спрашивать · 2 книги, 41 глава');
    // The strip stands in the place of the notices of the shell: after the top bar, before the page.
    expect(screen.getByText('Шапка').compareDocumentPosition(strip()!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(strip()!.compareDocumentPosition(screen.getByText('Страница')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(api.getIndexingStatus).toHaveBeenCalledWith('project-white-port', expect.anything());

    // The same readiness read again says nothing new.
    await act(async () => { await client.refetchQueries(); });
    expect(screen.getAllByRole('status', { name: /прочитан/u })).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Открыть чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port' });
    expect(strip()).toBeNull();
  });

  it('closes with its cross and remembers in the browser what it has told of', async () => {
    const first = renderWatcher('/saga', { projectId: 'project-white-port' }, () => statusOf({ at: readyAt, books: 1, chapters: 20 }));
    await waitFor(() => expect(strip()).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть уведомление' }));
    expect(strip()).toBeNull();
    expect(first.navigate).not.toHaveBeenCalled();
    cleanup();
    // The page is opened again: the memory of the module is gone, the record of the browser is not.
    forgetTellings();

    const again = renderWatcher('/saga', { projectId: 'project-white-port' }, () => statusOf({ at: readyAt, books: 1, chapters: 20 }));
    await waitFor(() => expect(again.api.getIndexingStatus).toHaveBeenCalled());
    await act(async () => { await again.client.refetchQueries(); });
    expect(strip()).toBeNull();
    cleanup();

    renderWatcher('/saga', { projectId: 'project-white-port' }, () => statusOf({ at: '2026-11-10T09:00:00.000Z', books: 5, chapters: 101 }));
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(strip()!.textContent).toContain('5 книг, 101 глава');
  });

  it('has no «Открыть чат» in the chat of the saga', async () => {
    renderWatcher('/chat', { projectId: 'project-white-port' }, () => statusOf({ at: readyAt, books: 1, chapters: 3 }));
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(strip()!.textContent).toContain('Книга прочитана — можно спрашивать · 1 книга, 3 главы');
    expect(screen.queryByRole('button', { name: 'Открыть чат' })).toBeNull();
  });

  it('says nothing of a saga the server does not call ready, and asks nothing without a saga', async () => {
    const quiet = renderWatcher('/saga', { projectId: 'project-white-port' }, () => statusOf());
    await waitFor(() => expect(quiet.api.getIndexingStatus).toHaveBeenCalled());
    expect(strip()).toBeNull();
    cleanup();

    // «Аккаунт» names no saga, and the shelf it has not read says nothing of one.
    const account = renderWatcher('/account', {}, () => statusOf({ at: readyAt, books: 1, chapters: 3 }));
    await act(async () => { await Promise.resolve(); });
    expect(account.api.getIndexingStatus).not.toHaveBeenCalled();
    expect(strip()).toBeNull();
  });

  // W-74: on the shelf its own line says the end; the strip stands aside there and is told all the same.
  it('stands aside on the shelf of the saga, where the line of the shelf says it, and is told there', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = renderWatcher('/manuscript/books', {}, () => statusOf({ at: readyAt, books: 3, chapters: 60 }), client);
    await act(async () => { await Promise.resolve(); });
    expect(view.api.getIndexingStatus).not.toHaveBeenCalled();

    // The shelf reads its saga: the watcher follows it.
    act(() => { client.setQueryData(manuscriptQueryKeys.shelf({ projectId: null }), { jobs: [], project: { id: 'project-default' } }); });
    await waitFor(() => expect(view.api.getIndexingStatus).toHaveBeenCalledWith('project-default', expect.anything()));
    await act(async () => { await client.refetchQueries(); });
    expect(strip()).toBeNull();
  });

  it('reads the status again when the shelf sees the work on the saga begin and end', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const shelf = (jobs: { kind: string }[]) => act(() => {
      client.setQueryData(manuscriptQueryKeys.shelf({ projectId: null }), { jobs, project: { id: 'project-default' } });
    });
    shelf([]);
    let status = statusOf();
    const { api, goTo } = renderWatcher('/chat', {}, () => status, client);
    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalledTimes(1));

    // A book is loaded: the cards of the saga are in work.
    shelf([{ kind: 'chapter_cards' }]);
    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalledTimes(2));
    expect(strip()).toBeNull();
    // Another Job of the same work changes nothing.
    shelf([{ kind: 'chapter_cards' }, { kind: 'canon_text_index' }]);
    // The thread layer is not the preparation of the text.
    status = statusOf({ at: readyAt, books: 1, chapters: 12 });
    shelf([{ kind: 'saga_layer' }]);
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(api.getIndexingStatus).toHaveBeenCalledTimes(3);
    expect(strip()!.textContent).toContain('Книга прочитана — можно спрашивать · 1 книга, 12 глав');
    goTo('/manuscript/books', {});
    expect(strip()).toBeNull();
  });

  // W-74: told on the shelf by its own line, the readiness is not told again on the next page of the tab.
  it('does not tell again on the next page what the shelf has told', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    act(() => { client.setQueryData(manuscriptQueryKeys.shelf({ projectId: null }), { jobs: [], project: { id: 'project-default' } }); });
    const { api, goTo } = renderWatcher('/manuscript/books', {}, () => statusOf({ at: readyAt, books: 1, chapters: 3 }), client);
    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalled());
    await act(async () => { await client.refetchQueries(); });
    // The shelf has heard of the readiness — the status reaches the page a tick after it is read, and a step to the chat
    // before that tick is a readiness first heard in the chat, which is told there (the flake of 11.11).
    await waitFor(() => expect(window.localStorage.getItem('canonkeeper.saga-ready.told') ?? '').toContain(readyAt));
    goTo('/chat', { projectId: 'project-default' });
    await act(async () => { await client.refetchQueries(); });
    expect(strip()).toBeNull();
  });
});
