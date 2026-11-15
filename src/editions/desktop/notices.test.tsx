// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SagaArchiveOpened } from '../../entities/saga-file/api';
import type { CanonKeeperApiClient } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { appOnTheComputer, noShell, shellOfTheWindow, type DesktopState } from './settings/settings.test-helpers';
import { updateAt } from './updates/update-test-helpers';

/** A fresh load of the page: the edition and the area of the toasts of its own load, the shell asked anew. */
async function freshLoad() {
  vi.resetModules();
  const [{ edition }, { ToastsProvider }, { sagaFileKeys }] = await Promise.all([
    import('.'), import('../../ui/feedback/Toast'), import('../../entities/saga-file/api'),
  ]);
  return { edition, ToastsProvider, sagaFileKeys };
}

let load: Awaited<ReturnType<typeof freshLoad>>;
beforeEach(async () => {
  // A new load of the page begins at an entry of the history no window of the previous load holds.
  window.history.replaceState(null, '', '/');
  load = await freshLoad();
});
afterEach(noShell);

/** A saga file opened from another page while the window stood: the answer of the server in the cache of the mutations. */
const opened: SagaArchiveOpened = {
  projectId: 'project-9', name: 'Хроники Белого порта', renamed: false,
  report: {
    books: 1, chapters: 4, cards: 4, chaptersWithoutCards: { count: 0, places: [] }, namesFresh: true, threads: false,
    materials: 0, memory: 0, notes: 0, chats: 0, chatsNotRead: [], coversSkipped: [],
    leftOut: { drafts: 0, drawings: 0, trashedBooks: 0, trashedChapters: 0, othersTurns: 0 }, sections: [],
  },
};

const firstRun: DesktopState = { ...appOnTheComputer, welcome: true, checkUpdates: true };
const restarted = 'CanonKeeper перезапущен после сбоя';

/**
 * What the root puts over the pages of the app (src/App.tsx): the notices of the account and its overlay, on the state of
 * the computer the test answers; the shell of the window says the backend was started again after a fall.
 */
async function renderTheRoot(getDesktopState: CanonKeeperApiClient['getDesktopState']) {
  const invoke = shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: true } });
  const api = {
    getDesktopState,
    getAppUpdate: vi.fn(async () => updateAt({ version: '1.3.0' })),
    dismissWelcome: vi.fn(async () => undefined),
  } as unknown as CanonKeeperApiClient;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await queryClient.getMutationCache().build(queryClient, { mutationKey: load.sagaFileKeys.open, mutationFn: async () => opened }).execute(undefined);
  const account = load.edition.useAccount({ api, navigate: vi.fn(), route: createAppRoute('/manuscript/books') } as unknown as Parameters<typeof load.edition.useAccount>[0]);
  const { ToastsProvider } = load;
  render(
    <QueryClientProvider client={queryClient}>
      <ToastsProvider>
        {account.notices}
        {account.overlay}
      </ToastsProvider>
    </QueryClientProvider>,
  );
  return { invoke };
}

/** Nothing of the notices stands, and the shell was not asked for the notes of the launch. */
async function expectNothingYet(invoke: ReturnType<typeof shellOfTheWindow>) {
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(invoke.mock.calls.map(([command]) => command)).not.toContain('launch_notes');
  expect(screen.queryByText(restarted)).toBeNull();
  expect(screen.queryByRole('dialog', { name: 'Сага открыта' })).toBeNull();
}

async function expectTheNotices() {
  expect(await screen.findByText(restarted)).toBeTruthy();
  expect(await screen.findByRole('dialog', { name: 'Сага открыта' })).toBeTruthy();
}

// specs/2026-11-15-desk-updates-web.md UW1.4: the lines and windows of the app that open by themselves — the notes of a
// launch, «Сага открыта» — wait for «Добро пожаловать» as the root puts them, by the one sign `welcome` of the server: while
// the window stands and while its state is read; a read that failed shows no window, and they do not wait for it.
describe('the notices of the app and «Добро пожаловать»', () => {
  it('wait while the window stands and come once «Начать» closed it', async () => {
    const states = [firstRun, { ...firstRun, welcome: false }];
    const { invoke } = await renderTheRoot(vi.fn(async () => (states.length > 1 ? states.shift()! : states[0]!)));
    const welcome = await screen.findByRole('dialog', { name: 'CanonKeeper на вашем компьютере' });
    await expectNothingYet(invoke);
    fireEvent.click(within(welcome).getByRole('button', { name: 'Начать' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'CanonKeeper на вашем компьютере' })).toBeNull());
    await expectTheNotices();
  });

  it('wait while the state of the window is read', async () => {
    let answer!: (state: DesktopState) => void;
    const { invoke } = await renderTheRoot(vi.fn(() => new Promise<DesktopState>((resolve) => { answer = resolve; })));
    await expectNothingYet(invoke);
    await act(async () => answer(appOnTheComputer));
    await expectTheNotices();
  });

  it('do not wait for a window that a failed read cannot show', async () => {
    await renderTheRoot(vi.fn(async () => {
      throw new Error('the state of the computer did not open');
    }));
    await expectTheNotices();
    expect(screen.queryByRole('dialog', { name: 'CanonKeeper на вашем компьютере' })).toBeNull();
  });
});
