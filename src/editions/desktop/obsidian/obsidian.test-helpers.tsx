// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import axe from 'axe-core';
import { Suspense, useState } from 'react';
import { vi } from 'vitest';
import { createMockApiClient, type MockScenarioControl } from '../../../api/mock';
import { AppShell } from '../../../app/shell/AppShell';
import type { CurrentUser } from '../../../entities/session/api';
import type { FolderChoice } from '../../../shared/desktop/shell';
import { createAppRoute, parseRoute, type AppRoute, type RouteNavigate } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { SagaSectionsProvider } from '../../../ui/saga-sections';
import { edition } from '..';

/** The computer of the app with its local author signed in and a saga of his, on the mock of its contract. */
export async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  const saga = await api.createProject({ title: 'Северный предел' });
  return { api, saga: saga.id };
}

export type TheApp = Awaited<ReturnType<typeof theApp>>['api'];

/** The shell of the window in the page: `pick_folder` answers what the test says; every command is recorded. */
export function withShell(pick: (request: string) => FolderChoice | Promise<FolderChoice> = () => ({ outcome: 'linked' })) {
  const calls: Array<[string, unknown]> = [];
  Object.assign(window, {
    __TAURI__: {
      core: {
        invoke: async (command: string, args?: { request?: string }) => {
          calls.push([command, args ?? null]);
          return command === 'pick_folder' ? pick(args?.request ?? '') : null;
        },
      },
    },
  });
  return calls;
}

export function withoutShell() {
  delete (window as { __TAURI__?: unknown }).__TAURI__;
}

const user = { id: 'user-local' } as CurrentUser;

/** A page of the edition at an address, which moves when the page navigates — as the router of the app. */
export function renderAt(api: TheApp, address: string, options: { client?: QueryClient; page?: 'manuscript' | 'memory'; notices?: boolean } = {}) {
  const client = options.client ?? new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const steps: Array<{ path: string; query: Record<string, string | undefined> | undefined; options: Parameters<RouteNavigate>[2] }> = [];
  let go: (route: AppRoute) => void = () => undefined;
  function Harness() {
    const [route, setRoute] = useState(() => parseRoute(`http://localhost${address}`));
    go = setRoute;
    const navigate: RouteNavigate = (path, query, stepOptions) => {
      steps.push({ path, query, options: stepOptions });
      setRoute(createAppRoute(path, query ?? {}));
    };
    return <>
      {edition.pages[options.page ?? 'manuscript']!.render({ api, navigate, route, user })}
      {options.notices ? <Notices navigate={navigate} route={route} /> : null}
    </>;
  }
  // What the edition puts after the pages — its notices — as the root of the app does (`edition.useAccount`).
  function Notices({ navigate, route }: { navigate: RouteNavigate; route: AppRoute }) {
    const account = edition.useAccount({
      api, navigate, route, requestedHref: '/', status: 'authenticated', user, unauthorizedFailure: null, serviceOutage: false,
      toasts: { push: () => 0, dismissAll: () => undefined },
    });
    return <>{account.notices}</>;
  }
  const view = render(
    <QueryClientProvider client={client}>
      <ToastsProvider><Suspense fallback={null}><Harness /></Suspense></ToastsProvider>
    </QueryClientProvider>,
  );
  return { ...view, client, steps, go: (to: string) => go(parseRoute(`http://localhost${to}`)) };
}

/**
 * A page of the edition as the root of the app puts it (src/App.tsx): over the pages and the shell the providers of the edition
 * (`wrapPages`), the one shell of the author around the page, the notices after them, one area of the toasts. The test file
 * makes `@edition` the edition of the app (`vi.mock('@edition', …)`), as `npm run build:desktop` does.
 */
export function renderInTheApp(api: TheApp, address: string, options: { client?: QueryClient } = {}) {
  const client = options.client ?? new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let go: (route: AppRoute) => void = () => undefined;
  function TheRoot() {
    const [route, setRoute] = useState(() => parseRoute(`http://localhost${address}`));
    go = setRoute;
    const navigate: RouteNavigate = (path, query) => setRoute(createAppRoute(path, query ?? {}));
    const account = edition.useAccount({
      api, navigate, route, requestedHref: '/', status: 'authenticated', user, unauthorizedFailure: null, serviceOutage: false,
      toasts: { push: () => 0, dismissAll: () => undefined },
    });
    const page = edition.pages[route.surface === 'memory' ? 'memory' : 'manuscript']!;
    return <>
      {account.wrapPages(
        <SagaSectionsProvider value={edition.sections}>
          <AppShell api={api} navigate={navigate} route={route} user={user}>
            <Suspense fallback={null}>{page.render({ api, navigate, route, user })}</Suspense>
          </AppShell>
        </SagaSectionsProvider>,
      )}
      {account.notices}
    </>;
  }
  const view = render(<QueryClientProvider client={client}><ToastsProvider><TheRoot /></ToastsProvider></QueryClientProvider>);
  return { ...view, client, go: (to: string) => go(parseRoute(`http://localhost${to}`)) };
}

/** The mock works the Jobs of a saga as the server would: every Job read until none is left in work. */
export async function settle(api: TheApp, saga: string) {
  for (let round = 0; round < 12; round += 1) {
    const live = (await api.listProjectJobs(saga)).data.filter((job) => !['succeeded', 'failed', 'canceled'].includes(job.status));
    if (!live.length) return;
    for (const job of live) await api.getJob(job.id);
  }
}

/** The books of folders of the example loaded from them, their load of «Книги» at its end. */
export async function loadFolders(api: TheApp, saga: string, folders: readonly string[], key: string) {
  const preview = await api.previewObsidianImport(saga, { books: [...folders] });
  await api.importObsidianBooks(saga, { books: preview.books.map((book) => ({ folder: book.folder, fingerprint: book.fingerprint })) }, { idempotencyKey: key });
  await settle(api, saga);
}

/** A saga of the app with the book of the folder «Книга 1» of the example, loaded from it: «Застава». */
export async function aBookOfItsFolder(scenario: Partial<MockScenarioControl> = {}) {
  const { api, saga } = await theApp(scenario);
  await api.beginObsidianVaultChoice(saga);
  await loadFolders(api, saga, ['Книга 1'], 'idem-words-books-01');
  const bookId = (await api.getObsidianVault(saga)).books![0]!.bookId;
  return { api, saga, bookId };
}

/** The menu of a book of «Книги», opened by its «⋯». */
export function menuOf(title: string) {
  fireEvent.click(screen.getByRole('button', { name: `Действия книги ${title}` }));
  return screen.getByRole('menu', { name: 'Действия книги' });
}

/** The lines of the one area of the toasts, read anew: the area moves into its place once the shell stands. */
export function toastsText() {
  return screen.queryAllByRole('region', { name: 'Уведомления' }).map((region) => region.textContent).join(' ');
}

/** Counts the calls of an operation of the mock, which still answers. */
export function countCalls<T extends object, K extends keyof T>(api: T, operation: K) {
  return vi.spyOn(api, operation as never);
}

/** What axe finds against WCAG 2.1 A and AA in a part of the page (colours are not read in jsdom). */
export async function axeViolations(container: Element) {
  // jsdom draws nothing: axe asks a canvas whether a glyph is an icon, and gets none instead of an error.
  const canvas = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
  try {
    const { violations } = await axe.run(container, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
      rules: { 'color-contrast': { enabled: false } },
    });
    return violations.map((each) => `${each.id}: ${each.nodes.slice(0, 3).map((node) => node.target.join(' ')).join(' ; ')}`);
  } finally {
    canvas.mockRestore();
  }
}

/** Waits until a predicate of the page holds. */
export function until(check: () => void) {
  return waitFor(check, { timeout: 10_000 });
}
