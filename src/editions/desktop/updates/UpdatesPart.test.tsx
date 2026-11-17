// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { noShell, shellOfTheWindow } from '../settings/settings.test-helpers';
import { appUpdateKey, updatePollMs, useAppUpdate } from './app-update';
import { refusal, release, updateAt } from './update-test-helpers';
import { checkedWhen, refusalWords, type AppUpdate } from './update-words';
import { UpdatesPart } from './UpdatesPart';

// The clock of the tests: 15 November 2026, 16:00 on the clock of the machine, whatever its time zone (the page writes the
// time of a check on the clock of the computer); the checks were today at 14:05 and yesterday at 14:05.
const now = new Date(2026, 10, 15, 16, 0);
const today = new Date(2026, 10, 15, 14, 5).toISOString();
const yesterday = new Date(2026, 10, 14, 14, 5).toISOString();
const path = '/home/author/Загрузки/CanonKeeper_1.3.0_amd64.deb';

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, toFake: ['Date'] });
  vi.setSystemTime(now);
});

afterEach(() => {
  vi.useRealTimers();
  noShell();
});

type Client = Pick<CanonKeeperApiClient, 'getAppUpdate' | 'checkAppUpdate' | 'installAppUpdate' | 'cancelAppUpdate' | 'updateAppUpdateSettings'>;

/** The part over the updates the server answers one read after another; its actions answer what the test gives. */
function renderPart(updates: AppUpdate[], client: Partial<Client> = {}) {
  const queue = [...updates];
  const getAppUpdate = vi.fn(async () => (queue.length > 1 ? queue.shift()! : queue[0]!));
  const api = {
    getAppUpdate,
    checkAppUpdate: vi.fn(async () => updateAt({ phase: 'checking', found: null })),
    installAppUpdate: vi.fn(async () => updateAt({ phase: 'downloading', found: null, available: release, percent: 0 })),
    cancelAppUpdate: vi.fn(async () => updateAt({ found: 'available', available: release })),
    updateAppUpdateSettings: vi.fn(async ({ daily }: { daily: boolean }) => updateAt({ daily })),
    ...client,
  } as unknown as CanonKeeperApiClient & Client;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider><UpdatesPart api={api} /></ToastsProvider></QueryClientProvider>,
  );
  return { api, getAppUpdate, queryClient };
}

type Want = 'shown' | string | null;

/** A button of the state as the table says it: it stands, it stands closed with its reason, or it is not there. */
function expectButton(name: string, want: Want) {
  const button = screen.queryByRole('button', { name });
  if (want === null) {
    expect(button, `«${name}» stands`).toBeNull();
    return;
  }
  expect(button, `«${name}» is not there`).not.toBeNull();
  if (want === 'shown') {
    expect(button!.getAttribute('aria-disabled'), `«${name}» is closed`).toBeNull();
  } else {
    expect(button!.getAttribute('aria-disabled')).toBe('true');
    expect(button!.getAttribute('aria-description')).toBe(want);
  }
}

interface Row {
  name: string;
  update: AppUpdate;
  words: string[];
  check: Want;
  install: Want;
  whatsNew?: Want;
  cancel?: Want;
  releases?: boolean;
  daily?: boolean | null;
  adminAsk?: boolean;
}

const unchanged = 'Приложение и данные не изменились';
const rows: Row[] = [
  { name: 'idle / latest', update: updateAt({ found: 'latest', checkedAt: today }), words: ['У вас последняя версия', 'Последняя проверка — сегодня в 14:05'], check: 'shown', install: null },
  { name: 'idle / available', update: updateAt({ found: 'available', available: release, checkedAt: today }), words: ['Доступна версия 1.3.0'], check: 'shown', install: 'shown', whatsNew: 'shown' },
  { name: 'idle / available, a package of Linux', update: updateAt({ kind: 'deb', found: 'available', available: release }), words: ['Доступна версия 1.3.0', 'Система спросит пароль администратора'], check: 'shown', install: 'shown', whatsNew: 'shown', adminAsk: true },
  { name: 'idle / available, rpm', update: updateAt({ kind: 'rpm', found: 'available', available: release }), words: ['Система спросит пароль администратора'], check: 'shown', install: 'shown', whatsNew: 'shown', adminAsk: true },
  { name: 'idle / available, nsis', update: updateAt({ kind: 'nsis', found: 'available', available: release }), words: ['Доступна версия 1.3.0'], check: 'shown', install: 'shown', whatsNew: 'shown', adminAsk: false },
  { name: 'idle / available, empty notes', update: updateAt({ found: 'available', available: { version: '1.3.0', notes: ' \n ' } }), words: ['Доступна версия 1.3.0'], check: 'shown', install: 'shown', whatsNew: null },
  { name: 'idle / offline', update: updateAt({ found: 'offline', checkedAt: yesterday }), words: ['Не удалось проверить, есть ли новая версия. Попробуйте позже', 'Последняя проверка — вчера в 14:05'], check: 'shown', install: null },
  { name: 'idle / never, not checked', update: updateAt({}), words: ['Ещё не проверялось'], check: 'shown', install: null },
  { name: 'idle / never, checked before', update: updateAt({ checkedAt: yesterday }), words: ['Последняя проверка — вчера в 14:05'], check: 'shown', install: null },
  { name: 'checking, no version', update: updateAt({ phase: 'checking', found: null }), words: ['Проверяю, есть ли новая версия…'], check: 'Проверка уже идёт', install: null },
  { name: 'checking, a version found before', update: updateAt({ phase: 'checking', found: null, available: release }), words: ['Проверяю, есть ли новая версия…', 'Доступна версия 1.3.0'], check: 'Проверка уже идёт', install: 'Проверка уже идёт', whatsNew: 'shown' },
  { name: 'downloading', update: updateAt({ phase: 'downloading', found: null, available: release, percent: 40 }), words: ['Скачиваю версию 1.3.0: 40 %'], check: null, install: null, cancel: 'shown' },
  { name: 'waiting_turn', update: updateAt({ phase: 'waiting_turn', found: null, available: release }), words: ['Идёт ответ помощника — обновлю, когда он закончится; ответ сохранится'], check: null, install: null, cancel: 'shown' },
  { name: 'installing', update: updateAt({ phase: 'installing', found: null, available: release }), words: ['Устанавливаю версию 1.3.0: сохраняю данные и останавливаю помощника. Приложение закроется и откроется снова'], check: null, install: null, cancel: null },
  { name: 'download_failed', update: updateAt({ phase: 'download_failed', found: null, available: release }), words: [`Не удалось скачать версию 1.3.0. ${unchanged}`], check: 'shown', install: 'shown' },
  { name: 'verify_failed', update: updateAt({ phase: 'verify_failed', found: null, available: release }), words: [`Скачанный файл версии 1.3.0 не прошёл проверку и не установлен. ${unchanged}`], check: 'shown', install: 'shown' },
  { name: 'install_failed without a package', update: updateAt({ kind: 'nsis', phase: 'install_failed', found: null, available: release }), words: [`Не удалось установить версию 1.3.0. ${unchanged}`], check: 'shown', install: 'shown' },
  { name: 'install_failed with the package kept', update: updateAt({ kind: 'deb', phase: 'install_failed', found: null, available: release, package: path }), words: [`Не удалось установить версию 1.3.0. ${unchanged}`, `Пакет новой версии сохранён: ${path} — его можно установить и самому`], check: 'shown', install: 'shown' },
  { name: 'install_refused / declined', update: updateAt({ kind: 'deb', phase: 'install_refused', found: null, available: release, refusal: 'declined', package: path }), words: [`Обновление не установлено: окно пароля администратора закрыто. Пакет новой версии сохранён: ${path} — его можно установить и самому`], check: 'shown', install: 'shown' },
  // specs/2026-11-17-desk-update-linux.md UL3.1: 127 is both no window of the password and no right.
  { name: 'install_refused / unavailable', update: updateAt({ kind: 'rpm', phase: 'install_refused', found: null, available: release, refusal: 'unavailable', package: path }), words: [`Система не спросила пароль администратора или не дала разрешения на установку. Пакет новой версии сохранён: ${path} — установите его сами`], check: 'shown', install: null },
  // UL3.1: another installation of programs — nothing kept, the retry once it ended.
  { name: 'install_refused / busy', update: updateAt({ kind: 'deb', phase: 'install_refused', found: null, available: release, refusal: 'busy' }), words: ['Обновление не установлено: на компьютере идёт другая установка программ. Дождитесь её конца и нажмите «Обновить и перезапустить» — система спросит пароль администратора'], check: 'shown', install: 'shown' },
  { name: 'kind: none', update: updateAt({ kind: 'none' }), words: ['Эта сборка обновляется вручную: новая версия — на странице выпусков'], check: null, install: null, releases: true, daily: null },
  { name: 'version: null', update: updateAt({ version: null, phase: 'downloading', found: null, available: null, percent: 10 }), words: ['Скачиваю новую версию: 10 %'], check: null, install: null, cancel: 'shown' },
];

// specs/2026-11-15-desk-updates-web.md UW2.1–UW2.5 (board 44·14): each state of the part — its words and its buttons, as
// the table «Состояния части» says; the page counts and remembers nothing, everything comes from the fields of the server.
describe('«Настройки → Обновления»', () => {
  it.each(rows)('$name: the words and the buttons of the table', async (row) => {
    renderPart([row.update]);
    for (const words of row.words) expect(await screen.findByText(words)).toBeTruthy();
    expectButton('Проверить сейчас', row.check);
    expectButton('Обновить и перезапустить', row.install);
    expectButton('Что нового', row.whatsNew ?? null);
    expectButton('Отменить обновление', row.cancel ?? null);
    expect(screen.queryByRole('link', { name: 'Открыть страницу выпусков' }) !== null).toBe(row.releases === true);
    if (row.adminAsk !== undefined) expect(screen.queryByText('Система спросит пароль администратора') !== null).toBe(row.adminAsk);
    // «Версия …» when there is one; the switch — but in a build updated by hand.
    expect(screen.queryByText('Версия 1.2.0') !== null).toBe(row.update.version !== null);
    const daily = screen.queryByRole('switch', { name: 'Проверять новые версии при запуске и раз в день' });
    if (row.daily === null) expect(daily).toBeNull();
    else expect(daily?.getAttribute('aria-checked')).toBe(String(row.update.daily));
  });

  // specs/2026-11-17-desk-update-linux.md UL3.1: every refusal of the contract has its words, and no other has.
  it('has the words of every refusal of the contract', () => {
    const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      components: { schemas: { AppUpdate: { properties: { refusal: { enum: (string | null)[] } } } } };
    };
    const refusals = contract.components.schemas.AppUpdate.properties.refusal.enum.filter((refusal) => refusal !== null);
    expect(Object.keys(refusalWords).sort()).toEqual([...refusals].sort());
  });

  // UW2.1 (the coordinator, 15.11): «Обновить и перезапустить» is the main button wherever it stands.
  it('makes «Обновить и перезапустить» the main button in the card and in every failure', async () => {
    for (const row of rows.filter((each) => each.install === 'shown')) {
      const view = renderPart([row.update]);
      const button = await screen.findByRole('button', { name: 'Обновить и перезапустить' });
      expect(button.getAttribute('data-variant'), row.name).toBe('primary');
      void view;
      document.body.innerHTML = '';
    }
  });

  it('says the check is off under the switch in any idle, and not in a phase', async () => {
    renderPart([updateAt({ daily: false, found: 'latest', checkedAt: today })]);
    expect(await screen.findByText('Приложение само не узнаёт о новых версиях. Проверить можно кнопкой')).toBeTruthy();
    expect(screen.getByText('У вас последняя версия')).toBeTruthy();
    document.body.innerHTML = '';
    renderPart([updateAt({ daily: false, phase: 'downloading', found: null, available: release, percent: 5 })]);
    await screen.findByText('Скачиваю версию 1.3.0: 5 %');
    expect(screen.queryByText(/Приложение само не узнаёт/u)).toBeNull();
  });

  it('writes the path of the package whole, as the server gave it, whatever the folder', async () => {
    const home = '/home/author/CanonKeeper_1.3.0_amd64 (3).deb';
    renderPart([updateAt({ kind: 'deb', phase: 'install_refused', found: null, available: release, refusal: 'declined', package: home })]);
    expect(await screen.findByText(new RegExp(home.replace(/[()]/gu, '\\$&'), 'u'))).toBeTruthy();
  });

  it('names a later version without a version as «новую версию»', async () => {
    renderPart([updateAt({ phase: 'download_failed', found: null, available: { version: '', notes: '' } })]);
    expect(await screen.findByText(`Не удалось скачать новую версию. ${unchanged}`)).toBeTruthy();
  });

  it('changes its words when the value of the server changes', async () => {
    const { queryClient } = renderPart([updateAt({ found: 'latest', checkedAt: today })]);
    await screen.findByText('У вас последняя версия');
    act(() => queryClient.setQueryData(appUpdateKey, updateAt({ phase: 'downloading', found: null, available: release, percent: 70 })));
    expect(await screen.findByText('Скачиваю версию 1.3.0: 70 %')).toBeTruthy();
    expect(screen.queryByText('У вас последняя версия')).toBeNull();
  });

  it('reads the update when the author enters it, and its actions put the answer of the server', async () => {
    const { api, getAppUpdate } = renderPart([updateAt({ found: 'available', available: release })]);
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить и перезапустить' }));
    expect(await screen.findByText('Скачиваю версию 1.3.0: 0 %')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Отменить обновление' }));
    expect(await screen.findByText('Доступна версия 1.3.0')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Проверить сейчас' }));
    expect(await screen.findByText('Проверяю, есть ли новая версия…')).toBeTruthy();
    expect([api.installAppUpdate, api.cancelAppUpdate, api.checkAppUpdate].map((call) => vi.mocked(call).mock.calls.length)).toEqual([1, 1, 1]);
    expect(getAppUpdate).toHaveBeenCalledTimes(1);
  });

  it('reads the phase again on 409 and says nothing', async () => {
    const { getAppUpdate } = renderPart(
      [updateAt({ found: 'available', available: release }), updateAt({ phase: 'checking', found: null, available: release })],
      { installAppUpdate: vi.fn(async () => { throw refusal(409, 'desktop.update_state'); }) },
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить и перезапустить' }));
    expect(await screen.findByText('Проверяю, есть ли новая версия…')).toBeTruthy();
    expect(getAppUpdate).toHaveBeenCalledTimes(2);
    expect(screen.queryByText('Не получилось — попробуйте ещё раз')).toBeNull();
  });

  it('says a refusal 429 in the area of the toasts, and the switch keeps the value of the server', async () => {
    renderPart([updateAt({ found: 'latest', checkedAt: today })], {
      updateAppUpdateSettings: vi.fn(async () => { throw refusal(429); }),
      checkAppUpdate: vi.fn(async () => { throw refusal(403, 'csrf.invalid'); }),
    });
    const daily = await screen.findByRole('switch', { name: 'Проверять новые версии при запуске и раз в день' });
    fireEvent.click(daily);
    const toasts = screen.getByRole('region', { name: 'Уведомления' });
    expect(await within(toasts).findByText('Не получилось — попробуйте ещё раз')).toBeTruthy();
    expect(daily.getAttribute('aria-checked')).toBe('true');
    fireEvent.click(screen.getByRole('button', { name: 'Проверить сейчас' }));
    await waitFor(() => expect(within(toasts).getAllByText('Не получилось — попробуйте ещё раз')).toHaveLength(2));
  });

  it('turns the daily check by the server and shows what the server answered', async () => {
    const { api } = renderPart([updateAt({ found: 'latest', checkedAt: today })]);
    const daily = await screen.findByRole('switch', { name: 'Проверять новые версии при запуске и раз в день' });
    fireEvent.click(daily);
    await waitFor(() => expect(daily.getAttribute('aria-checked')).toBe('false'));
    expect(api.updateAppUpdateSettings).toHaveBeenCalledWith({ daily: false });
    expect(screen.getByText('Приложение само не узнаёт о новых версиях. Проверить можно кнопкой')).toBeTruthy();
  });

  it('opens one window «Что нового в версии …»: an item a line without its mark, a scroll inside, «Закрыть» in its place', async () => {
    const notes = ['- Северный предел читается быстрее', '', '* Карта саги показывает дороги', '• Заметки ищутся по тексту', ...Array.from({ length: 40 }, (_, at) => `- Пункт ${at + 1}`)].join('\n');
    renderPart([updateAt({ found: 'available', available: { version: '1.3.0', notes } })]);
    fireEvent.click(await screen.findByRole('button', { name: 'Что нового' }));
    const window = await screen.findByRole('dialog', { name: 'Что нового в версии 1.3.0' });
    const items = within(window).getAllByRole('listitem').map((item) => item.textContent);
    expect(items.slice(0, 3)).toEqual(['Северный предел читается быстрее', 'Карта саги показывает дороги', 'Заметки ищутся по тексту']);
    expect(items).toHaveLength(43);
    // The text scrolls by the keyboard too: its region takes the focus (axe scrollable-region-focusable).
    const body = within(window).getByRole('region', { name: 'Что нового в версии 1.3.0' });
    expect(body.getAttribute('tabindex')).toBe('0');
    expect(within(body).getByRole('list')).toBeTruthy();
    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('sets one line of the notes as a paragraph', async () => {
    renderPart([updateAt({ found: 'available', available: { version: '1.3.0', notes: 'Первая версия CanonKeeper на компьютере.' } })]);
    fireEvent.click(await screen.findByRole('button', { name: 'Что нового' }));
    const window = await screen.findByRole('dialog', { name: 'Что нового в версии 1.3.0' });
    expect(within(window).queryByRole('list')).toBeNull();
    const line = within(window).getByText('Первая версия CanonKeeper на компьютере.');
    expect(line.tagName).toBe('P');
    // A long line scrolls inside the window and by the keyboard as a list does.
    const body = within(window).getByRole('region', { name: 'Что нового в версии 1.3.0' });
    expect(body.getAttribute('tabindex')).toBe('0');
    expect(body.contains(line)).toBe(true);
  });

  it('opens the permanent page of the releases in the browser of the system', async () => {
    const invoke = shellOfTheWindow();
    renderPart([updateAt({ kind: 'none' })]);
    fireEvent.click(await screen.findByRole('link', { name: 'Открыть страницу выпусков' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://github.com/canonkeeper/desktop-app/releases' }));
  });
});

// UW2.5: one function writes the time of the last check.
describe('the time of the last check', () => {
  it('says today, yesterday and an older day with the time', () => {
    const at = now.getTime();
    expect(checkedWhen(today, at)).toBe('сегодня в 14:05');
    expect(checkedWhen(yesterday, at)).toBe('вчера в 14:05');
    expect(checkedWhen(new Date(2026, 10, 13, 14, 5).toISOString(), at)).toBe('13 ноября в 14:05');
  });

  // «вчера» is the day before on the calendar, not 24 hours: where the clocks go back the day is 25 hours long (in New York
  // on 1 November 2026), and a check in its first hour is yesterday the next morning. A day of another year says its year.
  it('says yesterday by the calendar and the year of a check of another year', () => {
    const morning = new Date(2026, 10, 2, 0, 30).getTime();
    expect(checkedWhen(new Date(2026, 10, 1, 0, 30).toISOString(), morning)).toBe('вчера в 00:30');
    expect(checkedWhen(new Date(2026, 9, 31, 23, 59).toISOString(), morning)).toBe('31 октября в 23:59');
    expect(checkedWhen(new Date(2025, 10, 13, 14, 5).toISOString(), now.getTime())).toBe('13 ноября 2025 г. в 14:05');
    expect(checkedWhen(new Date(2025, 11, 31, 14, 5).toISOString(), new Date(2026, 0, 1, 9, 0).getTime())).toBe('вчера в 14:05');
  });
});

// UW2.4: one query of the update for the part, the strip and «Спросить»: every second while a work goes, else every minute,
// whoever reads it.
describe('the reads of the update', () => {
  it('follow it once a second while a work goes and once a minute else, however many read it', async () => {
    vi.useRealTimers();
    vi.useFakeTimers();
    const phases: AppUpdate[] = [updateAt({ phase: 'downloading', found: null, available: release, percent: 10 })];
    const getAppUpdate = vi.fn(async () => phases[0]!);
    const api = { getAppUpdate } as unknown as CanonKeeperApiClient;
    function Readers() {
      useAppUpdate(api, { follow: true });
      useAppUpdate(api);
      useAppUpdate(api);
      return null;
    }
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Readers /></QueryClientProvider>);
    await act(async () => vi.advanceTimersByTimeAsync(10));
    expect(getAppUpdate).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(3 * updatePollMs.going));
    expect(getAppUpdate).toHaveBeenCalledTimes(4);
    phases[0] = updateAt({ found: 'latest', checkedAt: today });
    await act(async () => vi.advanceTimersByTimeAsync(updatePollMs.going));
    expect(getAppUpdate).toHaveBeenCalledTimes(5);
    await act(async () => vi.advanceTimersByTimeAsync(updatePollMs.calm - updatePollMs.going));
    expect(getAppUpdate).toHaveBeenCalledTimes(5);
    await act(async () => vi.advanceTimersByTimeAsync(2 * updatePollMs.going));
    expect(getAppUpdate).toHaveBeenCalledTimes(6);
  });
});
