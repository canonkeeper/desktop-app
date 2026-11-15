// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import type { MockScenarioControl } from '../../../api/mock/scenarios';
import { AppShell } from '../../../app/shell/AppShell';
import type { CurrentUser } from '../../../entities/session/api';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { edition } from '..';

// The root of the tests is the edition of the desktop app, as `npm run build:desktop` makes it.
vi.mock('@edition', async () => await import('..'));
vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

const author = { id: 'local-author', displayName: 'Автор', email: 'local@desktop.invalid', emailVerified: true, documentsAccepted: true } as CurrentUser;

/** A window of `width`: what the media queries of the shell answer. */
function windowOf(width: number) {
  vi.stubGlobal('innerWidth', width);
  vi.stubGlobal('matchMedia', (query: string) => {
    const max = /\(max-(width|height): (\d+)px\)/u.exec(query);
    const matches = max ? (max[1] === 'width' ? width : 900) <= Number(max[2]) : false;
    return { matches, media: query, addEventListener: () => undefined, removeEventListener: () => undefined };
  });
}

beforeEach(() => windowOf(1440));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** «Книги» of the app in the one shell of the root, under what the account of the edition wraps the pages in. */
async function theShelf(scenario: Partial<MockScenarioControl>, prepare?: (api: ReturnType<typeof createMockApiClient>) => void) {
  const api = createMockApiClient();
  api.startDesktop();
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  api.setScenario({ ...api.getScenario(), ...scenario });
  prepare?.(api);
  const navigate = vi.fn();
  const route = createAppRoute('/manuscript/books');
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider>
        {account.wrapPages(<AppShell api={api} navigate={navigate} route={route} user={author}><h1>Книги</h1></AppShell>)}
      </ToastsProvider>
    </QueryClientProvider>,
  );
  const bar = screen.getByRole('banner');
  await within(bar).findByRole('button', { name: 'Настройки' });
  return { api, bar, navigate };
}

// specs/2026-11-15-desk-account-words-web.md AW4.7 (board 44·7): the tier of the linked app beside «Настройки» — its name the
// server's, its press «Настройки → Аккаунт»; none without a link, while nothing is told, with a broken link.
describe('the mark of the tier in the top bar of the app', () => {
  it.each([1440, 900])('names the tier of the account at %d px and opens «Настройки → Аккаунт»', async (width) => {
    windowOf(width);
    const { bar, navigate } = await theShelf({ accountLinked: true });
    const mark = await within(bar).findByRole('button', { name: 'Тариф «Писатель»' });
    expect(mark.textContent).toBe('Писатель');
    // It stands before «Настройки», which stay the last.
    const buttons = within(bar).getAllByRole('button');
    expect(buttons.indexOf(mark)).toBe(buttons.length - 2);
    fireEvent.click(mark);
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'account' });
  });

  it('follows the name the server gives', async () => {
    const { bar } = await theShelf({ accountLinked: true }, (api) => {
      const told = api.getAccount.bind(api);
      vi.spyOn(api, 'getAccount').mockImplementation(async () => {
        const account = await told();
        return account.account ? { ...account, account: { ...account.account, tier: { ...account.account.tier, title: 'Профи' } } } : account;
      });
    });
    const mark = await within(bar).findByRole('button', { name: 'Тариф «Профи»' });
    expect(mark.textContent).toBe('Профи');
  });

  it.each([
    ['not linked', {}],
    ['linked, nothing told yet', { accountLinked: true, accountInfoMissing: true }],
    ['the link broken', { accountBroken: true }],
  ] as const)('stands nowhere — %s', async (_case, scenario) => {
    const read = vi.fn();
    const { bar } = await theShelf(scenario, (api) => {
      const told = api.getAccount.bind(api);
      vi.spyOn(api, 'getAccount').mockImplementation(async () => {
        const account = await told();
        read(account.state);
        return account;
      });
    });
    // The link is read, and it says no tier.
    await waitFor(() => expect(read).toHaveBeenCalled());
    expect(within(bar).queryByRole('button', { name: /^Тариф/u })).toBeNull();
    expect(bar.textContent).not.toContain('Писатель');
  });

  it('is not in the top bar of a phone, which keeps its five places', async () => {
    windowOf(390);
    const { bar } = await theShelf({ accountLinked: true });
    await waitFor(() => expect(within(bar).getByRole('button', { name: 'Настройки' })).toBeTruthy());
    expect(within(bar).queryByRole('button', { name: /^Тариф/u })).toBeNull();
  });
});
