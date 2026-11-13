// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { Suspense } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { AppShell } from '../../../app/shell/AppShell';
import type { CurrentUser } from '../../../entities/session/api';
import { routeOfLocation } from '../../../routing/router';
import { createAppRoute, type AppRoute } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { edition } from '..';

// The root of the tests is the edition of the desktop app, as `npm run build:desktop` makes it.
vi.mock('@edition', async () => await import('..'));

const author = { id: 'local-author', displayName: 'Автор', email: 'local@desktop.invalid', emailVerified: true, documentsAccepted: true } as CurrentUser;

/** A window of `width`: what the media queries of the shell answer. */
function windowOf(width: number) {
  vi.stubGlobal('matchMedia', (query: string) => {
    const max = /\(max-(width|height): (\d+)px\)/u.exec(query);
    const matches = max ? (max[1] === 'width' ? width : 900) <= Number(max[2]) : false;
    return { matches, media: query, addEventListener: () => undefined, removeEventListener: () => undefined };
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The page of the address in the one shell of the root, as App puts it there for the app: no account to sign out of. */
async function openSettings(route: AppRoute) {
  const api = createMockApiClient();
  api.startDesktop();
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  const navigate = vi.fn();
  const page = edition.pages.settings!;
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider>
        <AppShell api={api} navigate={navigate} route={route} user={author}>
          <Suspense fallback={null}>{page.render({ api, navigate, route, user: author })}</Suspense>
        </AppShell>
      </ToastsProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('heading', { level: 1, name: 'Настройки' });
  return { api, navigate };
}

function partsNav() {
  return screen.getByRole('navigation', { name: 'Части настроек' });
}

// specs/2026-11-13-desk-settings.md ST1.1: «Настройки» stand in the place of the account menu; the app has no account, no
// «Выйти», no tier and no «Тема» in its top bar.
describe('«Настройки» of the app in the top bar', () => {
  it.each([1440, 900])('stand at the end of the top bar at %d px, marked on their page', async (width) => {
    windowOf(width);
    const { navigate } = await openSettings(createAppRoute('/settings'));
    const bar = screen.getByRole('banner');
    const settings = within(bar).getByRole('button', { name: 'Настройки' });
    // The board draws the same ghost button with the icon and the word in a row at 1440 and at 900 (`Settings`, 44·11).
    expect(settings.getAttribute('data-variant')).toBe('ghost');
    expect(settings.textContent).toBe('Настройки');
    expect(settings.getAttribute('aria-current')).toBe('page');
    expect(within(bar).queryByRole('button', { name: /^Аккаунт/u })).toBeNull();
    expect(bar.textContent).not.toMatch(/Выйти|Тема|Тариф/u);
    // On their own page they lead nowhere else.
    fireEvent.click(settings);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('open the page from any page of the app', async () => {
    windowOf(1440);
    const api = createMockApiClient();
    api.startDesktop();
    await api.openDesktopSession({ code: 'code-of-the-shell' });
    const navigate = vi.fn();
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ToastsProvider>
          <AppShell api={api} navigate={navigate} route={createAppRoute('/manuscript/books')} user={author}><h1>Книги</h1></AppShell>
        </ToastsProvider>
      </QueryClientProvider>,
    );
    const settings = within(screen.getByRole('banner')).getByRole('button', { name: 'Настройки' });
    expect(settings.getAttribute('aria-current')).toBeNull();
    fireEvent.click(settings);
    expect(navigate).toHaveBeenCalledWith('/settings');
  });
});

// ST1.2, ST1.3: the page — the parts on the left in the order of the board, the open part on the right, from the address.
describe('the page «Настройки» of the app', () => {
  it('names its three parts in the order of the board and opens «Данные» without a part in the address', async () => {
    windowOf(1440);
    await openSettings(createAppRoute('/settings'));
    const links = within(partsNav()).getAllByRole('link');
    expect(links.map((link) => link.textContent)).toEqual(['Данные', 'Вид', 'О программе']);
    expect(links.map((link) => link.getAttribute('href'))).toEqual(['/settings?part=data', '/settings?part=look', '/settings?part=about']);
    expect(links[0]!.getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('region', { name: 'Данные' })).toBeTruthy();
  });

  it('opens the part the address names and «Данные» for a part it does not know', async () => {
    windowOf(900);
    await openSettings(createAppRoute('/settings', { part: 'look' }));
    expect(within(partsNav()).getByRole('link', { name: 'Вид' }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('region', { name: 'Вид' })).toBeTruthy();
  });

  it('opens «Данные» for a part it does not know', async () => {
    windowOf(1440);
    await openSettings(createAppRoute('/settings', { part: 'account' }));
    expect(within(partsNav()).getByRole('link', { name: 'Данные' }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('region', { name: 'Данные' })).toBeTruthy();
  });

  it('puts the part chosen into the address', async () => {
    windowOf(1440);
    const { navigate } = await openSettings(createAppRoute('/settings'));
    fireEvent.click(within(partsNav()).getByRole('link', { name: 'О программе' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'about' }, { replace: true });
  });

  it('is a route of the app', () => {
    expect(edition.routes.map((route) => route.path)).toContain('/settings');
    expect(edition.pages.settings).toBeDefined();
    // The app has their page: its route of the address is «Настройки» with the part (the site's is no page).
    const route = routeOfLocation({ pathname: '/settings', searchStr: '?part=look', state: {} });
    expect(route.surface).toBe('settings');
    expect(route.query.get('part')).toBe('look');
  });
});
