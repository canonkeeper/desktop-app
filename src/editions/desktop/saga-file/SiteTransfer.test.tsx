// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { StrictMode, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import type { MockScenarioControl } from '../../../api/mock/scenarios';
import { AppShell } from '../../../app/shell/AppShell';
import type { CurrentUser } from '../../../entities/session/api';
import { createAppRoute, type RouteNavigate } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { edition } from '..';
import { accountQueryKey } from '../account/account-api';
import { sagaFileOfferSlot } from './SagaFileCard';
import { SagaFileSection } from './SagaFileSection';
import { siteTransferKey } from './transfer-api';
import { transferRefusals } from './transfer-words';

// The root of the tests is the edition of the desktop app, as `npm run build:desktop` makes it.
vi.mock('@edition', async () => await import('..'));
vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

const author = { id: 'local-author', displayName: 'Автор', email: 'local@desktop.invalid', emailVerified: true, documentsAccepted: true } as CurrentUser;

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: false, media: query, addEventListener: () => undefined, removeEventListener: () => undefined }));
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

type TheApp = ReturnType<typeof createMockApiClient>;

/** The computer of the app with its local author signed in, linked or not, and the scenario of the test. */
async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

/** What the edition of the app wraps its pages in — the link and the transfer among them — around `page`. */
function inTheApp(api: TheApp, page: (navigate: RouteNavigate) => ReactNode, query: Record<string, string> = {}) {
  const navigate = vi.fn<RouteNavigate>();
  const route = createAppRoute('/manuscript/books', query);
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ToastsProvider>{account.wrapPages(page(navigate))}</ToastsProvider>
    </QueryClientProvider>,
  );
  return { navigate, client };
}

/** The card of the first screen of the app, as the slot of the shelf puts it. */
function FirstScreenCard({ api }: { api: TheApp }) {
  return sagaFileOfferSlot(api)();
}

/** The saga of the computer in the one shell of the root: its menu and the line under its top bar. */
async function inTheShell(api: TheApp) {
  const saga = await api.createProject({ title: 'Хроники Белого порта' });
  const query = { projectId: saga.id };
  const result = inTheApp(api, (navigate) => (
    <AppShell api={api} navigate={navigate} route={createAppRoute('/manuscript/books', query)} user={author}><h1>Книги</h1></AppShell>
  ), query);
  await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
  return result;
}

async function openMenu() {
  fireEvent.click(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' }));
  return screen.findByRole('menu', { name: 'Выбор саги' });
}

const toasts = () => screen.getByRole('region', { name: 'Уведомления' });
/** A button closed by its reason stays in its place: `aria-disabled`, not `disabled` (PR1.1). */
const closed = (button: HTMLElement) => button.getAttribute('aria-disabled') === 'true';
/** The polls of the transfer go once a second: a step of the mock comes at the next one. */
const nextRead = { timeout: 3_000 };

// specs/2026-11-16-desk-site-saga.md TR6.1 (board 44·77, 44·78).
describe('the entries of the transfer of a saga from the site', () => {
  it('the card of the first screen of a linked app: «Перенести с сайта…» first, the file beside it, in the new words', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    const card = await screen.findByRole('region', { name: 'Перенести сагу с сайта или другого компьютера' });
    expect(within(card).getByText('Сага с сайта переедет одной кнопкой — книги и всё, что помощник о ней знает. Файл саги с другого компьютера откройте здесь же.')).toBeTruthy();
    await within(card).findByRole('button', { name: 'Перенести с сайта…' });
    expect(within(card).getAllByRole('button').map((button) => button.textContent)).toEqual(['Перенести с сайта…', 'Открыть файл саги…']);
    expect(within(card).queryByText('Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper')).toBeNull();
  });

  it('without a link the card and «Данные» say why and offer «Связать с аккаунтом»; no transfer anywhere', async () => {
    const api = await theApp();
    inTheApp(api, () => <><FirstScreenCard api={api} /><SagaFileSection api={api} /></>);
    const card = await screen.findByRole('region', { name: 'Перенести сагу с сайта или другого компьютера' });
    await within(card).findByText('Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper');
    expect(within(card).getAllByRole('button').map((button) => button.textContent)).toEqual(['Связать с аккаунтом', 'Открыть файл саги…']);
    const data = screen.getByRole('group', { name: 'Файл саги' });
    expect(within(data).getByText('Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper')).toBeTruthy();
    expect(within(data).getByRole('button', { name: 'Связать с аккаунтом' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Перенести/u })).toBeNull();
  });

  it('«Данные» of a linked app: «Перенести сагу с сайта…» beside the buttons of the file', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <SagaFileSection api={api} />);
    const data = screen.getByRole('group', { name: 'Файл саги' });
    await within(data).findByRole('button', { name: 'Перенести сагу с сайта…' });
    expect(within(data).getAllByRole('button').map((button) => button.textContent))
      .toEqual(['Сохранить сагу в файл…', 'Открыть файл саги…', 'Перенести сагу с сайта…']);
  });

  it('the menu of sagas: «Перенести сагу с сайта…» under «Новая сага» of a linked app, none without a link', async () => {
    const linked = await theApp({ accountLinked: true });
    await inTheShell(linked);
    const menu = await openMenu();
    await within(menu).findByRole('menuitem', { name: 'Перенести сагу с сайта…' });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent).slice(-3)).toEqual(['Новая сага', 'Перенести сагу с сайта…', 'Настроить сагу']);
    cleanup();
    const unlinked = await theApp();
    const { client } = await inTheShell(unlinked);
    // The menu is read once the link is known: an app without one has no item, not one still unknown.
    await waitFor(() => expect(client.getQueryData(accountQueryKey)).toMatchObject({ state: 'none' }));
    const without = await openMenu();
    expect(within(without).getAllByRole('menuitem').map((item) => item.textContent).slice(-2)).toEqual(['Новая сага', 'Настроить сагу']);
  });
});

// specs/2026-11-16-desk-site-saga.md TR6.2, TR6.3 (board 44·79, 44·80).
describe('the transfer of a saga from the site in the app', () => {
  it('starts on a press: the window of the waiting names the account and the term, the page of the site is a link; «Отменить» stops it silently', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    const window = await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    expect(within(window).getByText('В браузере открылась страница CanonKeeper. Войдите в аккаунт a…@example.test, выберите сагу и нажмите «Перенести в приложение».')).toBeTruthy();
    expect(within(window).getByText(/^Ссылка действует до \d\d:\d\d$/u)).toBeTruthy();
    const page = within(window).getByRole('link', { name: 'Открыть страницу снова' });
    expect(page.getAttribute('href')).toMatch(/^https:\/\/canonkeeper\.example\.test\/transfer\?port=24567&request=.+&state=/u);
    expect(page.getAttribute('target')).toBe('_blank');
    const { id } = await api.getSiteSagaTransfer();
    const cancel = vi.spyOn(api, 'cancelSiteSagaTransfer');
    fireEvent.click(within(window).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none', id: null }));
    // The stop and the outcome put away name the transfer of the window (TR2.2).
    expect(cancel.mock.calls).toEqual([[{ id }], [{ id }]]);
    expect(within(toasts()).queryByRole('status')).toBeNull();
    expect(within(toasts()).queryByRole('alert')).toBeNull();
  });

  it('× hides the window and the transfer waits on', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' })).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'waiting' });
  });

  it('goes through its phases under the top bar of any page and ends in «Сага открыта» once', async () => {
    const api = await theApp({ accountLinked: true });
    const { navigate } = await inTheShell(api);
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Перенести сагу с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    // The author gives the saga on the site: the return of the browser the mock has not.
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    const preparing = await screen.findByRole('region', { name: 'Сайт готовит сагу…' }, nextRead);
    expect(screen.queryByRole('dialog', { name: 'Перенесите сагу на сайте' })).toBeNull();
    // The window closed because the saga began to come: the focus goes to the line of the work, not to the menu that is gone.
    await waitFor(() => expect(document.activeElement !== document.body && document.activeElement?.contains(preparing)).toBe(true));
    expect(within(preparing).getByText('До нескольких минут')).toBeTruthy();
    expect(closed(within(preparing).getByRole('button', { name: 'Остановить' }))).toBe(false);
    const coming = await screen.findByRole('region', { name: 'Скачиваю сагу «Хроники Белого порта»' }, nextRead);
    expect(within(coming).getByText('Скачиваю сагу «Хроники Белого порта» · 12,0 МБ')).toBeTruthy();
    const opening = await screen.findByRole('region', { name: 'Открываю сагу «Хроники Белого порта»' }, nextRead);
    expect(closed(within(opening).getByRole('button', { name: 'Остановить' }))).toBe(true);
    const done = await screen.findByRole('dialog', { name: 'Сага открыта' }, nextRead);
    expect(screen.queryByRole('region', { name: /сагу/u })).toBeNull();
    expect(within(done).getByText(/^Хроники Белого порта — с сайта от .+: 1 книга, 2 главы\. Помощник уже знает эту сагу — читать книги заново не нужно$/u)).toBeTruthy();
    // The notes of a transfer are the site's (TR6.2): what stayed out stayed on the site, no word of a file or of a link.
    expect(within(done).getAllByRole('listitem').map((note) => note.textContent)).toEqual([
      expect.stringMatching(/^Сага с таким названием уже есть — эта названа „Хроники Белого порта — с сайта от .+“$/u),
      'Не перенёсся 1 чат: „Кто убил капитана“ — его не удалось прочитать на сайте',
      // specs/2026-11-19-desk-trust.md TW6.1.
      'Сага на сайте осталась как была — удалить её можно на сайте',
      'Главы в работе и рисунки остались на сайте — приложение их не открывает',
      'Не перенеслись 1 книга и 2 главы из „Недавно удалённых“ — они остались на сайте',
      'Не перенеслись 4 вопроса других участников саги',
    ]);
    fireEvent.click(within(done).getByRole('button', { name: /Перейти к саге/u }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: expect.any(String) });
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none' }));
    expect(screen.queryByRole('dialog', { name: 'Сага открыта' })).toBeNull();
  }, 15_000);

  it('while the saga comes, «Перенести с сайта…» stays closed by «Сага уже переносится»; «Остановить» stops it silently', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    const card = screen.getByRole('region', { name: 'Перенести сагу с сайта или другого компьютера' });
    await waitFor(() => expect(closed(within(card).getByRole('button', { name: 'Перенести с сайта…' }))).toBe(true), nextRead);
    expect(within(card).getByText('Сага уже переносится')).toBeTruthy();
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: undefined });
    await api.cancelSiteSagaTransfer({ id: (await api.getSiteSagaTransfer()).id! });
    await waitFor(() => expect(closed(within(card).getByRole('button', { name: 'Перенести с сайта…' }))).toBe(false), nextRead);
    expect(within(toasts()).queryByRole('alert')).toBeNull();
  });

  it('a failure is one line in the words of its code, said once and put away', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'expired' });
    expect(await within(toasts()).findByText('Ссылка на сайте устарела — начните перенос снова', undefined, nextRead)).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Перенесите сагу на сайте' })).toBeNull();
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none' }));
    expect(within(toasts()).getAllByText('Ссылка на сайте устарела — начните перенос снова')).toHaveLength(1);
  });

  it('a failure of this computer offers «Написать нам»', async () => {
    const api = await theApp({ accountLinked: true });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'unexpected' });
    expect(await within(toasts()).findByText('Не удалось перенести сагу', undefined, nextRead)).toBeTruthy();
    expect(within(toasts()).getByRole('link', { name: 'Написать нам' })).toBeTruthy();
  });

  it('a start the backend refused says why: a transfer that goes, an update, no link', async () => {
    const refused = async (code: string, words: string) => {
      const api = await theApp({ accountLinked: true, operationFailures: { startSiteSagaTransfer: { status: 409, message: 'refused', code } } });
      inTheApp(api, () => <FirstScreenCard api={api} />);
      fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
      expect(await within(toasts()).findByText(words)).toBeTruthy();
      expect(screen.queryByRole('dialog')).toBeNull();
      cleanup();
    };
    await refused('saga_transfer.running', 'Сага уже переносится');
    await refused('desktop.updating', 'Ставлю обновление — перенесите сагу после перезапуска');
    await refused('account.not_linked', 'Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper');
  });
});

/** Real time: the reads of the transfer go once a second, so a second and a half without one is no poll. */
const quiet = () => new Promise((resolve) => { setTimeout(resolve, 1_500); });

// specs/2026-11-16-desk-site-saga.md TR6.3: one reader, once a second only while the transfer waits, downloads or opens.
describe('the reads of the transfer', () => {
  it('none without a transfer, none after a failure put away; once a second while it waits', async () => {
    const api = await theApp({ accountLinked: true });
    const reads = vi.spyOn(api, 'getSiteSagaTransfer');
    inTheApp(api, () => <FirstScreenCard api={api} />);
    await waitFor(() => expect(reads).toHaveBeenCalled());
    const idle = reads.mock.calls.length;
    await quiet();
    expect(reads.mock.calls.length).toBe(idle);

    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    await waitFor(() => expect(reads.mock.calls.length).toBeGreaterThan(idle + 1), nextRead);

    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'expired' });
    await within(toasts()).findByText('Ссылка на сайте устарела — начните перенос снова', undefined, nextRead);
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none' }));
    const after = reads.mock.calls.length;
    await quiet();
    expect(reads.mock.calls.length).toBe(after);
  }, 15_000);

  it('none while «Сага открыта» stands and none once it is closed', async () => {
    const api = await theApp({ accountLinked: true });
    const reads = vi.spyOn(api, 'getSiteSagaTransfer');
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    const done = await screen.findByRole('dialog', { name: 'Сага открыта' }, { timeout: 8_000 });
    const shown = reads.mock.calls.length;
    await quiet();
    expect(reads.mock.calls.length).toBe(shown);
    fireEvent.click(within(done).getByRole('button', { name: 'Закрыть' }));
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none' }));
    const closed = reads.mock.calls.length;
    await quiet();
    expect(reads.mock.calls.length).toBe(closed);
  }, 20_000);

  // The end is said once a transfer even when the effect that says it runs again — React runs the effects of the pages of
  // the app twice on their mount (StrictMode of src/main.tsx), and pages mounted over the cache find the end in it.
  it('says an end once when its effect runs again over the cache', async () => {
    const api = await theApp({ accountLinked: true });
    await api.startSiteSagaTransfer();
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'expired' });
    const failed = await api.getSiteSagaTransfer();
    expect(failed).toMatchObject({ phase: 'failed', code: 'expired' });
    const navigate = vi.fn<RouteNavigate>();
    const route = createAppRoute('/manuscript/books');
    const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    client.setQueryData(siteTransferKey, failed);
    render(
      <StrictMode><QueryClientProvider client={client}>
        <ToastsProvider>{account.wrapPages(<FirstScreenCard api={api} />)}</ToastsProvider>
      </QueryClientProvider></StrictMode>,
    );
    await waitFor(async () => expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'none' }));
    await screen.findAllByText('Ссылка на сайте устарела — начните перенос снова');
    expect(within(toasts()).getAllByText('Ссылка на сайте устарела — начните перенос снова')).toHaveLength(1);
  });

  // «Сага открыта» closed stays closed even when the backend did not put the end away (its answer did not come).
  it('a closed «Сага открыта» does not come back while its end is not put away', async () => {
    const api = await theApp({
      accountLinked: true, operationFailures: { cancelSiteSagaTransfer: { status: 503, message: 'unavailable', code: 'service.unavailable' } },
    });
    inTheApp(api, () => <FirstScreenCard api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Перенести с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    const done = await screen.findByRole('dialog', { name: 'Сага открыта' }, { timeout: 8_000 });
    fireEvent.click(within(done).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await quiet();
    expect(screen.queryByRole('dialog', { name: 'Сага открыта' })).toBeNull();
    expect(await api.getSiteSagaTransfer()).toMatchObject({ phase: 'done' });
  }, 15_000);

  // A failure of the read of the transfer is its own, not a failure of the session (the root reads «кто автор» again only
  // for one that is not local).
  it('its failures are local', async () => {
    const api = await theApp({ accountLinked: true });
    const { client } = inTheApp(api, () => <FirstScreenCard api={api} />);
    await waitFor(() => expect(client.getQueryCache().find({ queryKey: siteTransferKey })).toBeTruthy());
    expect(client.getQueryCache().find({ queryKey: siteTransferKey })?.meta).toMatchObject({ outageScope: 'local' });
  });
});

// TR6.1, TR6.2 (the decisions of the pages, 16.11): the entries while a transfer goes.
describe('the entries while a transfer goes', () => {
  it('a transfer that waits: every entry shows its window again and starts nothing', async () => {
    const api = await theApp({ accountLinked: true });
    const starts = vi.spyOn(api, 'startSiteSagaTransfer');
    await inTheShell(api);
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Перенести сагу с сайта…' }));
    const window = await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    const { id } = await api.getSiteSagaTransfer();
    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Перенести сагу с сайта…' }));
    const again = await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    fireEvent.keyDown(again, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Перенести сагу с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    expect(starts).toHaveBeenCalledTimes(1);
    expect(await api.getSiteSagaTransfer()).toMatchObject({ id, phase: 'waiting' });
  });

  it('the item of the menu of sagas stays closed by «Сага уже переносится» while the saga comes', async () => {
    const api = await theApp({ accountLinked: true });
    const starts = vi.spyOn(api, 'startSiteSagaTransfer');
    await inTheShell(api);
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Перенести сагу с сайта…' }));
    await screen.findByRole('dialog', { name: 'Перенесите сагу на сайте' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    await screen.findByRole('region', { name: 'Сайт готовит сагу…' }, nextRead);
    // The transfer stays downloading: the mock moves it on no more.
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: undefined });
    const item = within(await openMenu()).getByRole('menuitem', { name: /^Перенести сагу с сайта…/u });
    expect(closed(item)).toBe(true);
    expect(item.textContent).toContain('Сага уже переносится');
    fireEvent.click(item);
    expect(starts).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('the words of a failed transfer', () => {
  it('has a line for every code of the contract and of no other; the author\'s own stop says nothing', () => {
    const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      components: { schemas: { SiteSagaTransfer: { properties: { code: { enum: (string | null)[] } } } } };
    };
    const codes = contract.components.schemas.SiteSagaTransfer.properties.code.enum.filter((code): code is string => code !== null);
    expect(Object.keys(transferRefusals).sort()).toEqual([...codes].sort());
    expect(transferRefusals.canceled).toBeNull();
    // The four lines of board 44·80 — «другой аккаунт» is the page of the site's, not the app's (the decision log).
    expect(transferRefusals.expired?.message).toBe('Ссылка на сайте устарела — начните перенос снова');
    expect(transferRefusals.unreachable?.message).toBe('Нет связи с сайтом — проверьте интернет и начните перенос снова');
    expect(transferRefusals.busy?.message).toBe('Сайт занят и не успел собрать сагу — начните перенос позже');
  });
});
