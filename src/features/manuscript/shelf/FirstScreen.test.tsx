import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { parseRoute } from '../../../shared/navigation/app-route';
import { AccountSessionContext, type AccountSession } from '../../../shared/session/account-session';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { ShelfSlotProvider, type ShelfSlot } from './shelf-slot';

// specs/2026-11-10-ux3-first-screen.md UC1: before the first saga — one screen, three ways in, one main button, no sections.

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** The ways of a build: «Посмотреть на примере» and «Вычитать мою главу», as cards of their own. */
const siteSlot: ShelfSlot = {
  useDemoOffer: () => ({ element: <section aria-label="Посмотреть на примере"><button type="button">Попробовать на примере</button></section>, preparing: false }),
};

function renderFirst(slot: ShelfSlot | null, session: AccountSession = { busy: false }) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
  const navigate = vi.fn();
  const route = parseRoute('http://localhost/manuscript/books');
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <AccountSessionContext.Provider value={session}>
        <ShelfSlotProvider value={slot}>
          <InShell api={api} navigate={navigate} route={route}><ManuscriptShelfPage api={api} navigate={navigate} route={route} /></InShell>
        </ShelfSlotProvider>
      </AccountSessionContext.Provider>
    </QueryClientProvider>,
  );
  return { api, navigate };
}

describe('the first screen (FS1.1, FS1.2)', () => {
  it('stands alone: «С чего начнём?», the ways in, one main button and no sections or search', async () => {
    renderFirst(siteSlot);
    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    expect(within(first).getByRole('heading', { level: 1, name: 'С чего начнём?' })).toBeTruthy();
    // «Добро пожаловать в CanonKeeper» — one line over the heading, once.
    expect(screen.getAllByText(/Добро пожаловать/u).map((node) => node.textContent)).toEqual(['Добро пожаловать в CanonKeeper']);
    expect(screen.queryByText(/Три шага/u)).toBeNull();
    const naming = within(first).getByRole('region', { name: 'Назовите сагу' });
    expect(within(naming).getByRole('textbox', { name: 'Название саги' })).toBeTruthy();
    expect(within(naming).getByText('Сага — это все ваши книги одной истории. Дальше — загрузите первую книгу: через пару минут по ней можно спрашивать')).toBeTruthy();
    expect(within(first).getByRole('region', { name: 'Посмотреть на примере' })).toBeTruthy();
    // The one main button of the screen.
    const main = [...document.querySelectorAll('main button[data-variant="primary"]')].map((button) => button.textContent);
    expect(main).toEqual(['Создать сагу']);
    // The shell without a saga: no column of the sections, no search, the top bar names the first saga (S1.4).
    expect(screen.queryByRole('navigation', { name: 'Разделы' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Поиск по книгам' })).toBeNull();
    expect(screen.getByRole('banner').textContent).toContain('Первая сага');
  });

  it('has no card of the example in a build without it', async () => {
    renderFirst(null);
    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    expect(within(first).getByRole('region', { name: 'Назовите сагу' })).toBeTruthy();
    expect(within(first).queryByRole('region', { name: 'Посмотреть на примере' })).toBeNull();
  });

  it('names the saga for an unverified address too: the line of the address stands once', async () => {
    renderFirst(siteSlot, { busy: false, emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() } });
    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    expect(within(first).getByRole('button', { name: 'Создать сагу' })).toHaveProperty('disabled', false);
    expect(screen.queryAllByText(/new-author@example\.test/u).length).toBeLessThanOrEqual(1);
  });
});
