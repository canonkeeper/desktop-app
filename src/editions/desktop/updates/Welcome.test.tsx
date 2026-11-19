// SPDX-License-Identifier: AGPL-3.0-only
import { focusManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { appOnTheComputer, noShell, shellOfTheWindow, type DesktopState } from '../settings/settings.test-helpers';
import { refusal, updateAt } from './update-test-helpers';
import { AfterWelcome } from './welcome-state';
import { Welcome } from './Welcome';
import type { AppUpdate } from './update-words';

afterEach(noShell);

const firstRun: DesktopState = { ...appOnTheComputer, welcome: true, checkUpdates: true };
const dialog = () => screen.findByRole('dialog', { name: 'CanonKeeper на вашем компьютере' });

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

/** The window over a page, on the states of the computer the test gives one read after another. */
function renderWelcome({ states = [firstRun], update = Promise.resolve(updateAt({})), dismiss = vi.fn(async () => undefined), extra }: {
  states?: DesktopState[];
  update?: Promise<AppUpdate>;
  dismiss?: CanonKeeperApiClient['dismissWelcome'];
  extra?: ReactNode;
} = {}) {
  const queue = [...states];
  const getDesktopState = vi.fn(async () => (queue.length > 1 ? queue.shift()! : queue[0]!));
  const getAppUpdate = vi.fn(() => update);
  const api = { getDesktopState, getAppUpdate, dismissWelcome: dismiss } as unknown as CanonKeeperApiClient;
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider>
        <main><button type="button">Книги</button></main>
        {extra ? <AfterWelcome api={api}>{extra}</AfterWelcome> : null}
        <Welcome api={api} />
      </ToastsProvider>
    </QueryClientProvider>,
  );
  return { getDesktopState, getAppUpdate, dismiss };
}

// specs/2026-11-15-desk-updates-web.md UW1.1–UW1.4 (board 44·1): «CanonKeeper на вашем компьютере» over the product until
// «Начать»; the page counts and remembers nothing — the window stands by `welcome` of the server and goes by its new read.
describe('«Добро пожаловать» of the app', () => {
  it('stands while the server says so: where the data lie, what goes out, the check with its switch, and «Начать» in the focus', async () => {
    renderWelcome();
    const window = await dialog();
    expect(within(window).getByRole('heading', { name: 'CanonKeeper на вашем компьютере' })).toBeTruthy();
    expect(within(window).getByText('Книги и всё, что вы создадите, лежат на этом компьютере')).toBeTruthy();
    expect(within(window).getByText(appOnTheComputer.dataDir)).toBeTruthy();
    // specs/2026-11-19-desk-trust.md TW2.3, TW2.4: what goes out — the whole chapters of a search of the whole saga too —, then
    // that the helper only reads, then the journal.
    const out = within(window).getByText('Наружу уходит то, что нужно помощнику: ваш вопрос, прежний разговор чата, места книг и главы, которые вы приложили, ваши материалы, когда вы их загружаете, а когда он ищет по всей саге или читает книги — главы целиком');
    expect([...out.parentElement!.children].map((line) => line.textContent)).toEqual([
      'Наружу уходит то, что нужно помощнику: ваш вопрос, прежний разговор чата, места книг и главы, которые вы приложили, ваши материалы, когда вы их загружаете, а когда он ищет по всей саге или читает книги — главы целиком',
      'Текст ваших книг помощник не меняет — он только читает его',
      'Журнал в «Настройках» показывает каждую отправку вашего текста',
    ]);
    const check = await within(window).findByRole('switch', { name: 'При запуске и раз в день приложение спрашивает, есть ли новая версия' });
    expect(check.getAttribute('aria-checked')).toBe('true');
    const start = within(window).getByRole('button', { name: 'Начать' });
    await waitFor(() => expect(document.activeElement).toBe(start));
    // specs/2026-11-14-desk-journal.md JR3.5: the line of the journal stands under what goes out.
    expect(within(window).getByText('Журнал в «Настройках» показывает каждую отправку вашего текста')).toBeTruthy();
    // UW1.3: no word of «Письмо» or the voice — this build has none of them.
    expect(window.textContent).not.toMatch(/Письм|голос|пишете/u);
  });

  it('is not there once the welcome is said', async () => {
    const { getDesktopState } = renderWelcome({ states: [appOnTheComputer] });
    await waitFor(() => expect(getDesktopState).toHaveBeenCalled());
    await act(async () => undefined);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('closes by nothing but «Начать»: not Esc, not «Назад», not a click beside it', async () => {
    renderWelcome();
    const window = await dialog();
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.keyDown(document.body, { key: 'Escape' });
    const scrim = document.querySelector('[data-overlay-scrim]')!;
    fireEvent.pointerDown(scrim, { target: scrim });
    await act(async () => {
      await new Promise<void>((resolve) => {
        globalThis.window.addEventListener('popstate', () => resolve(), { once: true });
        globalThis.window.history.back();
      });
    });
    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(screen.getByRole('dialog', { name: 'CanonKeeper на вашем компьютере' })).toBeTruthy();
  });

  it('sends the choice of the switch, is busy while the answer goes and closes by the new read of the server', async () => {
    const answer = deferred<void>();
    const dismiss = vi.fn(() => answer.promise);
    const { getDesktopState } = renderWelcome({ states: [firstRun, { ...firstRun, welcome: false, checkUpdates: false }], dismiss });
    const window = await dialog();
    fireEvent.click(await within(window).findByRole('switch'));
    expect(within(window).getByRole('switch').getAttribute('aria-checked')).toBe('false');
    const start = within(window).getByRole('button', { name: 'Начать' });
    fireEvent.click(start);
    await waitFor(() => expect(dismiss).toHaveBeenCalledWith({ checkUpdates: false }));
    await waitFor(() => expect(start.getAttribute('aria-busy')).toBe('true'));
    fireEvent.click(start);
    expect(dismiss).toHaveBeenCalledTimes(1);
    // The window does not close by the memory of the page: it waits for the read after the answer.
    expect(screen.getByRole('dialog')).toBeTruthy();
    await act(async () => answer.resolve());
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(getDesktopState).toHaveBeenCalledTimes(2);
  });

  it('stays when the server did not answer «Начать» yet with the state, and keeps its refusal inside itself', async () => {
    const dismiss = vi.fn(async () => {
      throw refusal(429);
    });
    renderWelcome({ dismiss });
    const window = await dialog();
    fireEvent.click(within(window).getByRole('button', { name: 'Начать' }));
    const line = await within(window).findByRole('alert');
    expect(line.textContent).toBe('Не получилось сохранить выбор — попробуйте ещё раз');
    expect(screen.getByRole('dialog', { name: 'CanonKeeper на вашем компьютере' })).toBeTruthy();
  });

  it('gives a build updated by hand the way to the releases instead of the switch and keeps the choice of the server', async () => {
    const invoke = shellOfTheWindow();
    const dismiss = vi.fn(async () => undefined);
    renderWelcome({ states: [{ ...firstRun, checkUpdates: false }], update: Promise.resolve(updateAt({ kind: 'none' })), dismiss });
    const window = await dialog();
    expect(await within(window).findByText('Эта сборка обновляется вручную: новая версия — на странице выпусков')).toBeTruthy();
    expect(within(window).queryByRole('switch')).toBeNull();
    fireEvent.click(within(window).getByRole('link', { name: 'Открыть страницу выпусков' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://github.com/canonkeeper/desktop-app/releases' }));
    fireEvent.click(within(window).getByRole('button', { name: 'Начать' }));
    await waitFor(() => expect(dismiss).toHaveBeenCalledWith({ checkUpdates: false }));
  });

  it('waits for the kind of the build in the place of the switch, and shows the switch when the read failed', async () => {
    const update = deferred<AppUpdate>();
    renderWelcome({ update: update.promise });
    const window = await dialog();
    expect(within(window).queryByRole('switch')).toBeNull();
    expect(within(window).queryByText(/обновляется вручную/u)).toBeNull();
    await act(async () => update.reject(refusal(500)));
    expect(await within(window).findByRole('switch')).toBeTruthy();
  });

  it('reads the state once a load: the focus coming back to the window reads nothing again', async () => {
    const { getDesktopState } = renderWelcome();
    await dialog();
    act(() => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(getDesktopState).toHaveBeenCalledTimes(1);
    focusManager.setFocused(undefined);
  });

  it('keeps the lines and windows that open by themselves until it is closed', async () => {
    renderWelcome({ states: [firstRun, { ...firstRun, welcome: false }], extra: <p>Строка запуска</p> });
    const window = await dialog();
    expect(screen.queryByText('Строка запуска')).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: 'Начать' }));
    expect(await screen.findByText('Строка запуска')).toBeTruthy();
  });
});
