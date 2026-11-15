// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock/scenarios';
import { accountQuotaQueryKeys } from '../../../entities/account-quota/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import type { CanonKeeperApiClient, OperationRequest } from '../../../shared/api';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { desktopStateQueryKey } from '../settings/desktop-state';
import { aiSettingsQueryKey } from '../settings/helper/helper-api';
import { AboutPart } from '../settings/AboutPart';
import { noShell, shellOfTheWindow } from '../settings/settings.test-helpers';
import { remainingWords } from './account-words';
import { AccountLinkProvider } from './AccountLink';
import { AccountPart } from './AccountPart';
import { renderWithLink, returnAs, theApp } from './account.test-helpers';

afterEach(() => {
  noShell();
  vi.useRealTimers();
});

const waitingTitle = 'Подтвердите связь в браузере: там открылась страница CanonKeeper';
const profile: NonNullable<OperationRequest<'updateAiSettings'>['own']> = {
  preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat',
  advanced: { contextWindow: 64_000, maxOutput: 16_000, reasoning: 'high', parallel: 2, priceInPerMillion: 0.27, priceOutPerMillion: 1.1, currency: 'RUB' },
};

/** The part «Аккаунт» as the place of «Связать…» (AW2.1). */
function accountPart(api: Awaited<ReturnType<typeof theApp>>) {
  return <AccountPart api={api as unknown as CanonKeeperApiClient} />;
}

function theWindow() {
  return screen.getByRole('dialog', { name: waitingTitle });
}

// specs/2026-11-15-desk-account-link-web.md AW1.1, AW1.2: one start — the address of /link in the browser of the system and the
// window of the waiting over the page —; ×, Esc and «Назад» hide the window, the link waits on and its line stands in the place
// of «Связать…»; only «Отменить» cancels it.
describe('the window of the waiting', () => {
  it('opens the address in the browser of the system, hides by × and cancels only by «Отменить»', async () => {
    const invoke = shellOfTheWindow();
    const api = await theApp();
    const started = vi.spyOn(api, 'startAccountLink');
    const canceled = vi.spyOn(api, 'cancelAccountLink');
    renderWithLink(api, accountPart(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    const window = await screen.findByRole('dialog', { name: waitingTitle });
    expect(started).toHaveBeenCalledWith({ chooseHelper: false });
    const address = (await api.getAccount()).link!.address;
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: address }));
    expect((within(window).getByRole('textbox', { name: 'Адрес страницы связи' }) as HTMLInputElement).value).toBe(address);
    expect(within(window).getByText('Сайт увидит имя этого компьютера «Рабочий стол», систему и версию приложения')).toBeTruthy();
    expect(within(window).getByText('Осталось 9 минут')).toBeTruthy();
    // The browser opened: the hint is quiet and «Открыть ещё раз» opens it again.
    expect(within(window).queryByRole('note')).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: 'Открыть ещё раз' }));
    await waitFor(() => expect(invoke.mock.calls.filter(([command]) => command === 'open_external')).toHaveLength(2));

    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть окно связи' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: waitingTitle })).toBeNull());
    expect(canceled).not.toHaveBeenCalled();
    expect((await api.getAccount()).state).toBe('waiting');
    // The line of the waiting in the place of the button, with «Открыть окно связи».
    expect(screen.getByText('Ждёт подтверждения в браузере · Осталось 9 минут')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Связать с аккаунтом' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть окно связи' }));
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: waitingTitle })).toBeNull());
    expect(canceled).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Открыть окно связи' }));
    fireEvent.click(within(theWindow()).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(canceled).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole('button', { name: 'Связать с аккаунтом' })).toBeTruthy();
    expect((await api.getAccount()).state).toBe('none');
  });

  // No refusal of an action is silent: «Отменить» the server did not take says so, and the link still waits.
  it('says a cancel the server did not take', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.startAccountLink({ chooseHelper: false });
    vi.spyOn(api, 'cancelAccountLink').mockRejectedValueOnce(new MockApiError(500, 'the shell left', 'cancelAccountLink', 'internal'));
    renderWithLink(api, accountPart(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Открыть окно связи' }));
    fireEvent.click(within(theWindow()).getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Не получилось — попробуйте ещё раз')).toBeTruthy();
    expect(await screen.findByText('Ждёт подтверждения в браузере · Осталось 9 минут')).toBeTruthy();
  });

  it('copies the address and says it in a line', async () => {
    shellOfTheWindow();
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const api = await theApp();
    renderWithLink(api, accountPart(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: waitingTitle })).getByRole('button', { name: 'Скопировать адрес' }));
    expect(await screen.findByText('Адрес скопирован')).toBeTruthy();
    expect(writeText).toHaveBeenCalledWith((await api.getAccount()).link!.address);
  });

  it('says the browser did not open — the shell refused it — and «Открыть ещё раз» is a link', async () => {
    const invoke = shellOfTheWindow();
    invoke.mockImplementation(async (command: string) => {
      if (command === 'open_external') throw 'not opened';
      return null;
    });
    const api = await theApp();
    renderWithLink(api, accountPart(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    const window = await screen.findByRole('dialog', { name: waitingTitle });
    // The refusal of the shell comes after the window opened: the warning is announced (P6 of the review).
    expect(await within(window).findByRole('alert')).toHaveProperty('textContent', 'Браузер не открылся? Скопируйте адрес и откройте его сами');
    expect(within(window).getByRole('link', { name: 'Открыть ещё раз' }).getAttribute('href')).toBe((await api.getAccount()).link!.address);
  });

  it('says it at once without the shell, where «Открыть ещё раз» is an ordinary link to a new tab', async () => {
    noShell();
    const api = await theApp();
    renderWithLink(api, accountPart(api));
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    const window = await screen.findByRole('dialog', { name: waitingTitle });
    expect(within(window).getByRole('note').textContent).toBe('Браузер не открылся? Скопируйте адрес и откройте его сами');
    const again = within(window).getByRole('link', { name: 'Открыть ещё раз' });
    expect([again.getAttribute('href'), again.getAttribute('target')]).toEqual([(await api.getAccount()).link!.address, '_blank']);
  });

  it('reads the link again on account.link_unavailable and shows the waiting it says', async () => {
    shellOfTheWindow();
    const api = await theApp();
    renderWithLink(api, accountPart(api));
    const button = await screen.findByRole('button', { name: 'Связать с аккаунтом' });
    // Another window of the same app started it meanwhile.
    await api.startAccountLink({ chooseHelper: false });
    fireEvent.click(button);
    expect(await screen.findByRole('dialog', { name: waitingTitle })).toBeTruthy();
    expect(screen.queryByText('Связь не началась. Попробуйте ещё раз')).toBeNull();
  });

  it('says the term in minutes by the server, and «Осталось меньше минуты» in the last one', () => {
    const now = Date.parse('2026-11-15T12:00:00Z');
    const at = (seconds: number) => new Date(now + seconds * 1_000).toISOString();
    expect([600, 599, 540, 179, 119, 61, 60, 59, 1].map((seconds) => remainingWords(at(seconds), now))).toEqual([
      'Осталось 9 минут', 'Осталось 9 минут', 'Осталось 8 минут', 'Осталось 2 минуты', 'Осталась 1 минута', 'Осталась 1 минута',
      'Осталось меньше минуты', 'Осталось меньше минуты', 'Осталось меньше минуты',
    ]);
  });
});

/**
 * The keys a link made reads again (AW1.3): the status of a saga, the counters of the tier and the state of the app with its
 * network (AW3.6); the settings are read anew.
 */
function readsAgain(client: QueryClient) {
  const keys = [[...manuscriptQueryKeys.all, 'indexing-status', 'saga-north'], accountQuotaQueryKeys.all, desktopStateQueryKey] as const;
  for (const key of keys) client.setQueryData(key, {});
  client.setQueryData(aiSettingsQueryKey, {});
  return () => [...keys.map((key) => client.getQueryState(key)?.isInvalidated), client.getQueryState(aiSettingsQueryKey)?.dataUpdateCount];
}

// AW1.3, AW3.2: the end of a waiting is a line of the one area of the toasts on any page — the observer of the edition reads the
// link while it waits; a link made says the address with its mask and who answers by the settings the server chose.
describe('the end of the waiting on another page', () => {
  it('says a link made with the helper of the account and reads the status, the settings and the quota again', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.startAccountLink({ chooseHelper: false });
    const client = renderWithLink(api, <p>Полка саги</p>);
    const invalidated = readsAgain(client);
    await screen.findByText('Полка саги');
    await waitFor(() => expect(client.getQueryData(['canonkeeper', 'desktop-account'])).toBeTruthy());
    returnAs(api, 'linked');
    expect(await screen.findByText('Приложение связано с аккаунтом a…\u2060@example.test. Отвечает помощник CanonKeeper', undefined, { timeout: 4_000 })).toBeTruthy();
    // The settings were read again: the line says who answers by them.
    expect(invalidated()).toEqual([true, true, true, 2]);
  });

  it('says «Отвечает ваш помощник» when «Свой ИИ» stays, and «Выбрать помощника CanonKeeper» sends the choice with its profile', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: profile });
    await api.startAccountLink({ chooseHelper: false });
    const chosen = vi.spyOn(api, 'updateAiSettings');
    // Another page — another part of «Настройки» too: the line chooses at once (no form of «Свой ИИ» to lose).
    renderWithLink(api, <p>Чат саги</p>, undefined, createAppRoute('/settings', { part: 'account' }));
    await screen.findByText('Чат саги');
    await new Promise((resolve) => setTimeout(resolve, 50));
    returnAs(api, 'linked');
    const line = await screen.findByText('Приложение связано с аккаунтом a…\u2060@example.test. Отвечает ваш помощник', undefined, { timeout: 4_000 });
    fireEvent.click(within(line.closest('[role="status"], [role="alert"]') as HTMLElement).getByRole('button', { name: 'Выбрать помощника CanonKeeper' }));
    await waitFor(() => expect(chosen).toHaveBeenCalledTimes(1));
    expect(chosen.mock.calls[0]![0]).toMatchObject({ source: 'account', own: { preset: 'deepseek', chatModel: 'deepseek-chat', advanced: { contextWindow: 64_000, currency: 'RUB', priceInPerMillion: 0.27 } } });
    expect((await api.getAiSettings()).settings).toMatchObject({ source: 'account', own: { preset: 'deepseek', advanced: { contextWindow: 64_000 } } });
  });

  it.each([
    ['expired', 'system', 'Связать не получилось: время на подтверждение вышло'],
    ['exchange', 'system', 'Связать не получилось: сайт не подтвердил связь'],
    ['keyring', 'system', 'Связь не сохранилась: хранилище паролей системы не ответило'],
    ['keyring', 'file', 'Связь не сохранилась: не получилось записать её в папку данных'],
  ] as const)('says a link that failed by %s (the pass in the %s store) with «Связать снова»', async (outcome, store, words) => {
    shellOfTheWindow();
    const api = await theApp({ accountPassStore: store });
    await api.startAccountLink({ chooseHelper: true });
    const started = vi.spyOn(api, 'startAccountLink');
    const client = renderWithLink(api, <p>Карта саги</p>);
    await screen.findByText('Карта саги');
    await new Promise((resolve) => setTimeout(resolve, 50));
    // The list of the network of «О программе» is read again after any end (AW3.6): the site is not asked any more.
    client.setQueryData(desktopStateQueryKey, {});
    returnAs(api, outcome);
    const line = await screen.findByText(words, undefined, { timeout: 4_000 });
    expect(client.getQueryState(desktopStateQueryKey)?.isInvalidated).toBe(true);
    api.setScenario({ ...api.getScenario(), accountLinkOutcome: undefined });
    fireEvent.click(within(line.closest('[role="status"], [role="alert"]') as HTMLElement).getByRole('button', { name: 'Связать снова' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: false }));
    expect(await screen.findByRole('dialog', { name: waitingTitle })).toBeTruthy();
  });

  // AW1.3: no refusal of an action is silent — «Выбрать помощника CanonKeeper» of the line that the server did not take says so
  // (no connection with the server has its own words with «Повторить», the test of the part «Помощник»).
  it('says a choice of the helper from the line the server did not take', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: profile });
    await api.startAccountLink({ chooseHelper: false });
    renderWithLink(api, <p>Чат саги</p>);
    await screen.findByText('Чат саги');
    await new Promise((resolve) => setTimeout(resolve, 50));
    returnAs(api, 'linked');
    const line = await screen.findByText('Приложение связано с аккаунтом a…\u2060@example.test. Отвечает ваш помощник', undefined, { timeout: 4_000 });
    vi.spyOn(api, 'updateAiSettings').mockRejectedValueOnce(new MockApiError(500, 'the shell left', 'updateAiSettings', 'internal'));
    fireEvent.click(within(line.closest('[role="status"], [role="alert"]') as HTMLElement).getByRole('button', { name: 'Выбрать помощника CanonKeeper' }));
    expect(await screen.findByText('Не получилось — попробуйте ещё раз')).toBeTruthy();
  });

  // The pages render again as the author goes (a new address, a new page): the end is said once, not at every render.
  it('says the end once while the pages render again', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.startAccountLink({ chooseHelper: false });
    function Pages() {
      const [page, setPage] = useState(0);
      return (
        <AccountLinkProvider api={api as unknown as CanonKeeperApiClient}>
          <button onClick={() => setPage(page + 1)} type="button">{`Страница ${page}`}</button>
        </AccountLinkProvider>
      );
    }
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider><Pages /></ToastsProvider></QueryClientProvider>);
    await screen.findByText('Страница 0');
    await new Promise((resolve) => setTimeout(resolve, 50));
    returnAs(api, 'linked');
    await screen.findByText(/^Приложение связано с аккаунтом/u, undefined, { timeout: 4_000 });
    for (let page = 0; page < 3; page += 1) {
      fireEvent.click(screen.getByRole('button', { name: /^Страница/u }));
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(screen.getAllByText(/^Приложение связано с аккаунтом/u)).toHaveLength(1);
  });

  it('says nothing for a link that ended before the page opened', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.startAccountLink({ chooseHelper: false });
    returnAs(api, 'linked');
    await api.getAccount();
    renderWithLink(api, <p>Полка саги</p>);
    await screen.findByText('Полка саги');
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(screen.queryByText(/Приложение связано/u)).toBeNull();
  });
});

// AW3.6 (board 44·16): «Куда приложение ходит в сеть» names the server CanonKeeper by the line of getDesktopState — while the
// link waits, and with the questions and the reading once the helper of the account answers —, read again as the link goes.
describe('the network in «О программе»', () => {
  it('names the site while the link waits and the helper of the account once linked', async () => {
    shellOfTheWindow();
    const api = await theApp();
    renderWithLink(api, <><AccountPart api={api as unknown as CanonKeeperApiClient} /><AboutPart api={api as unknown as CanonKeeperApiClient} /></>);
    expect(await screen.findByText('Сейчас никуда')).toBeTruthy();
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    expect(await screen.findByText('canonkeeper.example.test — связь с аккаунтом: имя, тариф и остатки')).toBeTruthy();
    returnAs(api, 'linked');
    expect(await screen.findByText('canonkeeper.example.test — связь с аккаунтом: имя, тариф и остатки; вопросы помощнику и чтение книг', undefined, { timeout: 4_000 })).toBeTruthy();
  });
});
