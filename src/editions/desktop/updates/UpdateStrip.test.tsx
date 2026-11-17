// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Suspense, useCallback, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { createAppRoute, type AppRoute, type RouteNavigate } from '../../../shared/navigation/app-route';
import { reasonOf } from '../../../test/disabled-reason';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { TopBarNoticeProvider } from '../../../ui/navigation/TopBarStrip';
import { edition } from '..';
import { appUpdateKey } from './app-update';
import { release, updateAt } from './update-test-helpers';
import type { AppUpdate } from './update-words';
import { UpdateStrip } from './UpdateStrip';
import { UpdatesPart } from './UpdatesPart';

const path = '/home/author/Загрузки/CanonKeeper_1.3.0_amd64.deb';

beforeEach(() => {
  window.localStorage.clear();
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.localStorage.clear();
});

/** The strip alone over the update the test puts into the one query. */
function renderStrip(update: AppUpdate, route: AppRoute = createAppRoute('/manuscript/books')) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queryClient.setQueryData(appUpdateKey, update);
  const navigate = vi.fn();
  const api = {
    getAppUpdate: vi.fn(async () => update),
    installAppUpdate: vi.fn(async () => updateAt({ phase: 'downloading', found: null, available: release, percent: 0 })),
    cancelAppUpdate: vi.fn(async () => updateAt({ found: 'available', available: release })),
    checkAppUpdate: vi.fn(),
    updateAppUpdateSettings: vi.fn(),
  } as unknown as CanonKeeperApiClient;
  const onWhatsNew = vi.fn();
  const view = render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <UpdateStrip api={api} navigate={navigate} onWhatsNew={onWhatsNew} route={route} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { ...view, api, navigate, onWhatsNew, queryClient };
}

const strip = () => screen.queryByRole('region', { name: 'Обновление CanonKeeper' });
const stripFound = () => screen.findByRole('region', { name: 'Обновление CanonKeeper' });

/** The name of the cross of a strip of a failure. */
const failureCross = 'Скрыть сообщение';

/** The strip on the shelf and the part «Обновления» of the same page over the one query: the author retries from the part. */
function renderStripAndPart(update: AppUpdate) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queryClient.setQueryData(appUpdateKey, update);
  const api = {
    getAppUpdate: vi.fn(async () => queryClient.getQueryData<AppUpdate>(appUpdateKey)!),
    installAppUpdate: vi.fn(async () => updateAt({ phase: 'downloading', found: null, available: release, percent: 0 })),
    checkAppUpdate: vi.fn(async () => updateAt({ phase: 'checking', found: null, available: release })),
    cancelAppUpdate: vi.fn(),
    updateAppUpdateSettings: vi.fn(),
  } as unknown as CanonKeeperApiClient;
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <UpdateStrip api={api} navigate={vi.fn()} onWhatsNew={vi.fn()} route={createAppRoute('/manuscript/books')} />
      <UpdatesPart api={api} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, queryClient };
}

interface StripRow {
  name: string;
  update: AppUpdate;
  message: string;
  buttons: string[];
  /** The name of the cross of the strip; null — no cross. */
  cross: string | null;
}

const versionCross = 'Скрыть до следующей версии';

const stripRows: StripRow[] = [
  { name: 'a version found', update: updateAt({ found: 'available', available: release }), message: 'Доступна версия 1.3.0', buttons: ['Что нового', 'Обновить и перезапустить'], cross: versionCross },
  { name: 'a version found without notes', update: updateAt({ found: 'available', available: { version: '1.3.0', notes: '' } }), message: 'Доступна версия 1.3.0', buttons: ['Обновить и перезапустить'], cross: versionCross },
  { name: 'a download', update: updateAt({ phase: 'downloading', found: null, available: release, percent: 40 }), message: 'Скачиваю версию 1.3.0: 40 %', buttons: ['Отменить обновление'], cross: null },
  { name: 'the wait for the answer', update: updateAt({ phase: 'waiting_turn', found: null, available: release }), message: 'Идёт ответ помощника — обновлю, когда он закончится', buttons: ['Отменить обновление'], cross: null },
  { name: 'not downloaded', update: updateAt({ phase: 'download_failed', found: null, available: release }), message: 'Не удалось скачать версию 1.3.0', buttons: ['Открыть обновления'], cross: failureCross },
  { name: 'the file not proved', update: updateAt({ phase: 'verify_failed', found: null, available: release }), message: 'Файл версии 1.3.0 не прошёл проверку', buttons: ['Открыть обновления'], cross: failureCross },
  { name: 'not installed', update: updateAt({ phase: 'install_failed', found: null, available: release, package: path }), message: 'Не удалось установить версию 1.3.0', buttons: ['Открыть обновления'], cross: failureCross },
  { name: 'the window of the password closed', update: updateAt({ kind: 'deb', phase: 'install_refused', found: null, available: release, refusal: 'declined', package: path }), message: 'Обновление до версии 1.3.0 не установлено: окно пароля администратора закрыто', buttons: ['Открыть обновления'], cross: failureCross },
  // specs/2026-11-17-desk-update-linux.md UL3.1 (board 44·15): the words of the part, the package with them; another installation.
  { name: 'no password or no right', update: updateAt({ kind: 'rpm', phase: 'install_refused', found: null, available: release, refusal: 'unavailable', package: path }), message: `Система не спросила пароль администратора или не дала разрешения на установку. Пакет новой версии сохранён: ${path} — установите его сами`, buttons: ['Открыть обновления'], cross: failureCross },
  { name: 'another installation of programs', update: updateAt({ kind: 'deb', phase: 'install_refused', found: null, available: release, refusal: 'busy' }), message: 'Обновление до версии 1.3.0 не установлено: идёт другая установка программ', buttons: ['Открыть обновления'], cross: failureCross },
];

const allButtons = ['Что нового', 'Обновить и перезапустить', 'Отменить обновление', 'Открыть обновления'];

// specs/2026-11-15-desk-updates-web.md UW3.1, UW3.2 (board 44·15): the strip of an update in the words of the Data — its
// buttons, its cross where the Data gives one, a region named «Обновление CanonKeeper».
describe('the strip of an update', () => {
  it.each(stripRows)('$name: the words, the buttons and the cross of the Data', async (row) => {
    renderStrip(row.update);
    const line = strip()!;
    expect(line).not.toBeNull();
    expect(within(line).getByText(row.message)).toBeTruthy();
    for (const name of allButtons) expect(within(line).queryByRole('button', { name }) !== null, name).toBe(row.buttons.includes(name));
    for (const cross of [versionCross, failureCross]) expect(within(line).queryByRole('button', { name: cross }) !== null, cross).toBe(row.cross === cross);
  });

  it('says nothing where the Data has no words: nothing found, the latest, a check, the installation, a build by hand', () => {
    for (const update of [
      updateAt({}), updateAt({ found: 'latest' }), updateAt({ found: 'offline' }), updateAt({ phase: 'checking', found: null, available: release }),
      updateAt({ phase: 'installing', found: null, available: release }), updateAt({ kind: 'none' }),
    ]) {
      const view = renderStrip(update);
      expect(strip(), `${update.phase}/${update.found}/${update.kind}`).toBeNull();
      view.unmount();
    }
  });

  it('names the password of the administrator for a package of Linux', () => {
    renderStrip(updateAt({ kind: 'deb', found: 'available', available: release }));
    expect(within(strip()!).getByText('спросит пароль администратора')).toBeTruthy();
    cleanupAndRender(updateAt({ kind: 'appimage', found: 'available', available: release }));
    expect(within(strip()!).queryByText('спросит пароль администратора')).toBeNull();
  });

  it('shows the download as a bar and says aloud only a change of the state, not each per cent', async () => {
    const { queryClient } = renderStrip(updateAt({ phase: 'downloading', found: null, available: release, percent: 40 }));
    const bar = within(strip()!).getByRole('progressbar', { name: 'Скачиваю версию 1.3.0' });
    expect(bar.getAttribute('value')).toBe('0.4');
    const heard = document.querySelector('[aria-live="polite"]')!;
    expect(heard.textContent).toBe('Скачиваю версию 1.3.0');
    act(() => queryClient.setQueryData(appUpdateKey, updateAt({ phase: 'downloading', found: null, available: release, percent: 80 })));
    expect(await within(strip()!).findByText('Скачиваю версию 1.3.0: 80 %')).toBeTruthy();
    expect(heard.textContent).toBe('Скачиваю версию 1.3.0');
    act(() => queryClient.setQueryData(appUpdateKey, updateAt({ phase: 'waiting_turn', found: null, available: release })));
    await waitFor(() => expect(heard.textContent).toBe('Идёт ответ помощника — обновлю, когда он закончится'));
  });

  it('installs and cancels by the server, opens the part and «Что нового»', async () => {
    const { api, navigate, onWhatsNew } = renderStrip(updateAt({ found: 'available', available: release }));
    // The window itself is held around the pages: the row of the strip builds itself anew (the test of the edition below).
    fireEvent.click(within(strip()!).getByRole('button', { name: 'Что нового' }));
    expect(onWhatsNew).toHaveBeenCalledWith({ version: '1.3.0', notes: release.notes });
    fireEvent.click(within(strip()!).getByRole('button', { name: 'Обновить и перезапустить' }));
    expect(await screen.findByText('Скачиваю версию 1.3.0: 0 %')).toBeTruthy();
    fireEvent.click(within(strip()!).getByRole('button', { name: 'Отменить обновление' }));
    await waitFor(() => expect(within(strip()!).getByText('Доступна версия 1.3.0')).toBeTruthy());
    expect([vi.mocked(api.installAppUpdate).mock.calls.length, vi.mocked(api.cancelAppUpdate).mock.calls.length]).toEqual([1, 1]);
    expect(navigate).not.toHaveBeenCalled();
    const failed = cleanupAndRender(updateAt({ phase: 'download_failed', found: null, available: release }));
    fireEvent.click(within(strip()!).getByRole('button', { name: 'Открыть обновления' }));
    expect(failed.navigate).toHaveBeenCalledWith('/settings', { part: 'updates' });
  });

  it('hides a version until the next one, and a failure of a version until another failure or version', () => {
    renderStrip(updateAt({ found: 'available', available: release }));
    fireEvent.click(within(strip()!).getByRole('button', { name: versionCross }));
    expect(strip()).toBeNull();
    // The choice lives in the storage of the window: a new load does not bring the strip back.
    cleanupAndRender(updateAt({ found: 'available', available: release }));
    expect(strip()).toBeNull();
    const next = renderOnly(updateAt({ found: 'available', available: { version: '1.4.0', notes: '' } }));
    expect(within(strip()!).getByText('Доступна версия 1.4.0')).toBeTruthy();
    next.unmount();
    // A failure of the hidden version still shows: it is not the strip of the version.
    const failed = renderOnly(updateAt({ phase: 'download_failed', found: null, available: release }));
    fireEvent.click(within(strip()!).getByRole('button', { name: failureCross }));
    expect(strip()).toBeNull();
    failed.unmount();
    renderOnly(updateAt({ phase: 'verify_failed', found: null, available: release }));
    expect(within(strip()!).getByText('Файл версии 1.3.0 не прошёл проверку')).toBeTruthy();
  });

  // UW3.2, UW3.5: the author hid a failure, retried from «Настройки → Обновления» — the download, the wait, the installation —
  // and it failed again: the window is back on the first page, and the strip says so at once. A new check shows it too; the
  // version hidden stays hidden until the next one.
  it('hides a failure until a new attempt: a retry or a new check of the part shows the next failure of the same version', async () => {
    const failed = updateAt({ phase: 'install_failed', found: null, available: release });
    const { api } = renderStripAndPart(failed);
    fireEvent.click(within(strip()!).getByRole('button', { name: failureCross }));
    expect(strip()).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить и перезапустить' }));
    expect(await within(await stripFound()).findByText('Скачиваю версию 1.3.0: 0 %')).toBeTruthy();
    expect(api.installAppUpdate).toHaveBeenCalledTimes(1);
    // The installation failed again and the window came back: a new load of the page.
    cleanupAndRender(failed);
    expect(within(strip()!).getByText('Не удалось установить версию 1.3.0')).toBeTruthy();

    // A new check of the part brings a hidden failure back as well.
    const again = cleanupAndRenderStripAndPart(updateAt({ phase: 'download_failed', found: null, available: release }));
    fireEvent.click(within(strip()!).getByRole('button', { name: failureCross }));
    expect(strip()).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Проверить сейчас' }));
    await waitFor(() => expect(again.api.checkAppUpdate).toHaveBeenCalledTimes(1));
    act(() => again.queryClient.setQueryData(appUpdateKey, updateAt({ phase: 'download_failed', found: null, available: release })));
    expect(await within(await stripFound()).findByText('Не удалось скачать версию 1.3.0')).toBeTruthy();

    // The cross of a version found keeps it hidden until the next version, whatever the author pressed.
    const found = cleanupAndRenderStripAndPart(updateAt({ found: 'available', available: release }));
    fireEvent.click(within(strip()!).getByRole('button', { name: versionCross }));
    fireEvent.click(await screen.findByRole('button', { name: 'Проверить сейчас' }));
    await waitFor(() => expect(found.api.checkAppUpdate).toHaveBeenCalledTimes(1));
    act(() => found.queryClient.setQueryData(appUpdateKey, updateAt({ found: 'available', available: release })));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(strip()).toBeNull();
  });

  it('stands when the storage of the window is not there', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('denied'); });
    renderStrip(updateAt({ found: 'available', available: release }));
    expect(strip()).not.toBeNull();
    fireEvent.click(within(strip()!).getByRole('button', { name: versionCross }));
    expect(strip()).toBeNull();
  });

  it('is not on the part «Обновления», which says all of it itself', () => {
    renderStrip(updateAt({ found: 'available', available: release }), createAppRoute('/settings', { part: 'updates' }));
    expect(strip()).toBeNull();
    cleanupAndRender(updateAt({ found: 'available', available: release }), createAppRoute('/settings', { part: 'data' }));
    expect(strip()).not.toBeNull();
  });
});

/** A new load of the page with the strip and the part. */
function cleanupAndRenderStripAndPart(update: AppUpdate) {
  document.body.innerHTML = '';
  return renderStripAndPart(update);
}

/** Another strip on the same page: the choices of the window stay. */
function renderOnly(update: AppUpdate) {
  return renderStrip(update);
}

/** The page of the test goes and another one comes in its place: a new load of the strip. */
function cleanupAndRender(update: AppUpdate, route?: AppRoute) {
  document.body.innerHTML = '';
  return renderStrip(update, route);
}

/**
 * A page of the app in its shell under what the account of the edition wraps the pages in, as the root of the web puts them
 * (src/App.tsx): «Книга прочитана» of the root may come into the row under the top bar; the address moves as the author goes.
 */
function AppPage({ api, start }: { api: ReturnType<typeof createMockApiClient>; start: AppRoute }) {
  const [route, setRoute] = useState(start);
  const [read, setRead] = useState(false);
  const navigate = useCallback<RouteNavigate>((to, query = {}) => setRoute(createAppRoute(to, query)), []);
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  const user = { id: 'user-local' } as Parameters<NonNullable<typeof edition.pages.chat>['render']>[0]['user'];
  return (
    <TopBarNoticeProvider value={read ? <p data-saga-ready="">Книга прочитана — можно спрашивать</p> : null}>
      <button onClick={() => setRead(true)} type="button">Книга прочитана</button>
      {account.wrapPages(
        <InShell api={api} navigate={navigate} route={route}>
          {route.surface === 'chat' ? <Suspense fallback={null}>{edition.pages.chat!.render({ api, navigate, route, user })}</Suspense> : <p>Страница</p>}
        </InShell>,
      )}
    </TopBarNoticeProvider>
  );
}

function renderApp(update: AppUpdate, start: AppRoute) {
  const api = createMockApiClient();
  vi.spyOn(api, 'getAppUpdate').mockResolvedValue(update);
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><ToastsProvider><AppPage api={api} start={start} /></ToastsProvider></QueryClientProvider>);
  return api;
}

/** The strip stands in the row under the top bar of the shell: right after the top bar, out of the page. */
function expectUnderTheTopBar(line: HTMLElement) {
  const shell = line.closest('[data-shell]');
  expect(shell, 'the strip is not in the shell').not.toBeNull();
  expect(line.closest('main')).toBeNull();
  const children = [...shell!.children];
  const bar = children.findIndex((child) => child.matches('[data-shell-top-bar]'));
  const row = children.findIndex((child) => child.contains(line));
  expect(children[row] === line || children[row]!.matches('[data-top-bar-strip]'), 'the strip is not in the row of the strips').toBe(true);
  // Between the top bar and the strip only the place of its announcement.
  expect(bar).toBeGreaterThanOrEqual(0);
  expect(children.slice(bar + 1, row).every((child) => child.matches('[aria-live]'))).toBe(true);
}

// UW3.1, UW3.4, UW3.5: around the pages of the app — the strip under the top bar on the shelf, in the chat and the other
// parts of «Настройки»; «Спросить» closed while the app installs, the reason of the server first.
describe('the update around the pages of the app', () => {
  it('puts the strip under the top bar of the shelf and of another part of «Настройки»', async () => {
    renderApp(updateAt({ phase: 'install_failed', found: null, available: release }), createAppRoute('/manuscript/books', { projectId }));
    const line = await screen.findByRole('region', { name: 'Обновление CanonKeeper' });
    expectUnderTheTopBar(line);
    expect(within(line).getByText('Не удалось установить версию 1.3.0')).toBeTruthy();
    document.body.innerHTML = '';
    renderApp(updateAt({ found: 'available', available: release }), createAppRoute('/settings', { part: 'about' }));
    expectUnderTheTopBar(await screen.findByRole('region', { name: 'Обновление CanonKeeper' }));
  });

  // UW2.2, UW3.1: the row under the top bar builds itself anew when «Книга прочитана» comes; the strip stands over it, and
  // «Что нового» opened from the strip stays open.
  it('stands over «Книга прочитана» and keeps «Что нового» opened from it while the row is rebuilt', async () => {
    renderApp(updateAt({ found: 'available', available: release }), createAppRoute('/manuscript/books', { projectId }));
    fireEvent.click(within(await stripFound()).getByRole('button', { name: 'Что нового' }));
    const notes = await screen.findByRole('dialog', { name: 'Что нового в версии 1.3.0' });
    expect(within(notes).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Северный предел читается быстрее', 'Карта саги показывает дороги']);
    fireEvent.click(screen.getByRole('button', { name: 'Книга прочитана', hidden: true }));
    const ready = await screen.findByText('Книга прочитана — можно спрашивать');
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(screen.getByRole('dialog', { name: 'Что нового в версии 1.3.0' })).toBeTruthy();
    const line = screen.getByRole('region', { name: 'Обновление CanonKeeper', hidden: true });
    expectUnderTheTopBar(line);
    expect(line.parentElement!.matches('[data-top-bar-strip]')).toBe(true);
    expect(line.compareDocumentPosition(ready) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Что нового в версии 1.3.0' })).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('closes «Спросить» while the app installs and says why; the reason of the server comes first', async () => {
    renderApp(updateAt({ phase: 'installing', found: null, available: release }), createAppRoute('/chat', { projectId, new: '1' }));
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    expect(await screen.findByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    expect(strip()).toBeNull();
    const field = within(chat).getByRole('textbox', { name: 'Вопрос' });
    fireEvent.change(field, { target: { value: 'Кто остановил лампу маяка?' } });
    const ask = within(chat).getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect(reasonOf(ask)).toBe('Ставлю обновление — спросите после перезапуска'));
    expect(ask.getAttribute('aria-disabled')).toBe('true');
    document.body.innerHTML = '';

    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
    vi.spyOn(api, 'getAppUpdate').mockResolvedValue(updateAt({ phase: 'installing', found: null, available: release }));
    // The pages of the app stand under the one area of the toasts of the root: the way to the helper of the app says its result there.
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider><AppPage api={api} start={createAppRoute('/chat', { projectId, new: '1' })} /></ToastsProvider></QueryClientProvider>);
    const second = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    fireEvent.change(within(second).getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто остановил лампу маяка?' } });
    await waitFor(() => expect(reasonOf(within(second).getByRole('button', { name: 'Спросить' }))).toBe('Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас'));
  });

  it('asks nothing in any other phase', async () => {
    // A helper chosen: without one «Спросить» says that the helper is not connected (decision 15.11, account-words-web).
    const api = createMockApiClient();
    await api.updateAiSettings({ source: 'own', own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' } });
    vi.spyOn(api, 'getAppUpdate').mockResolvedValue(updateAt({ phase: 'waiting_turn', found: null, available: release }));
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider><AppPage api={api} start={createAppRoute('/chat', { projectId, new: '1' })} /></ToastsProvider></QueryClientProvider>);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    fireEvent.change(within(chat).getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто остановил лампу маяка?' } });
    await screen.findByRole('region', { name: 'Обновление CanonKeeper' });
    expect(within(chat).getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled')).not.toBe('true');
  });
});
