// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CanonKeeperApiClient } from '../../shared/api';
import { noShell, shellOfTheWindow } from './settings/settings.test-helpers';
import { updateAt } from './updates/update-test-helpers';

afterEach(() => {
  noShell();
  vi.useRealTimers();
});

/** A fresh load of the page: the module asks the shell anew, in the area of the toasts of its own load. */
async function freshLoad() {
  vi.resetModules();
  const [{ LaunchNotes }, { ToastsProvider }] = await Promise.all([import('./launch-notes'), import('../../ui/feedback/Toast')]);
  return { LaunchNotes, ToastsProvider };
}

let LaunchNotes: Awaited<ReturnType<typeof freshLoad>>['LaunchNotes'];
let ToastsProvider: Awaited<ReturnType<typeof freshLoad>>['ToastsProvider'];
beforeEach(async () => {
  ({ LaunchNotes, ToastsProvider } = await freshLoad());
});

/** The lines of the launch over the update the server answers: the version that runs is 1.3.0 unless the test says. */
function renderNotes(version: string | null = '1.3.0') {
  const getAppUpdate = vi.fn(async () => updateAt({ version }));
  const api = { getAppUpdate } as unknown as CanonKeeperApiClient;
  const view = render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider><LaunchNotes api={api} /></ToastsProvider>
    </QueryClientProvider>,
  );
  return { ...view, getAppUpdate };
}

const toasts = () => screen.getByRole('region', { name: 'Уведомления' });

// specs/2026-11-13-desk-settings.md ST4.4 (board 44·22): once the window opens, the page asks the shell what happened and
// says it in the one area of the toasts — once a launch.
describe('the lines of a launch of the app', () => {
  it('say the app was restarted after a fall and the address of the window changed', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: true, restartedAfterCrash: true } });
    renderNotes();
    expect(await screen.findByText('CanonKeeper перезапущен после сбоя')).toBeTruthy();
    expect(screen.getByText('Адрес окна сменился: место чтения и вид текста сброшены')).toBeTruthy();
    expect(invoke).toHaveBeenCalledTimes(1);
  });

  // Board 44·22: each line has its icon in the colour of a notice — a restart `rotate-ccw`, the new address `info`.
  it('mark each line by its icon in the colour of a notice', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: true, restartedAfterCrash: true } });
    renderNotes();
    const restart = (await screen.findByText('CanonKeeper перезапущен после сбоя')).closest('[data-tone]')!;
    const address = screen.getByText('Адрес окна сменился: место чтения и вид текста сброшены').closest('[data-tone]')!;
    expect([restart.getAttribute('data-tone'), address.getAttribute('data-tone')]).toEqual(['info', 'info']);
    expect(restart.querySelector('svg.lucide-rotate-ccw')).not.toBeNull();
    expect(address.querySelector('svg.lucide-info')).not.toBeNull();
  });

  it('say only what happened', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: true } });
    renderNotes();
    expect(await screen.findByText('CanonKeeper перезапущен после сбоя')).toBeTruthy();
    expect(screen.queryByText(/Адрес окна сменился|обновлён/u)).toBeNull();
  });

  it('ask the shell once a load of the page, however often the root mounts them', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false } });
    const first = renderNotes();
    first.unmount();
    renderNotes();
    await vi.waitFor(() => expect(invoke).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('status')?.textContent ?? '').not.toMatch(/CanonKeeper|Адрес окна/u);
  });

  // The root may mount the notices again before the shell answered (a new author of the window, a reset of its state): the
  // page asks once, and the answer is said by the notices that stand when it comes.
  it('say what the shell answered to the notices mounted again before the answer, asked once', async () => {
    let answer!: (notes: unknown) => void;
    const invoke = vi.fn(() => new Promise((resolve) => { answer = resolve; }));
    (window as { __TAURI__?: unknown }).__TAURI__ = { core: { invoke } };
    const api = { getAppUpdate: vi.fn(async () => updateAt({ version: '1.3.0' })) } as unknown as CanonKeeperApiClient;
    function Root() {
      const [shown, setShown] = useState(true);
      return (
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ToastsProvider>
            <button onClick={() => setShown((now) => !now)} type="button">Сменить</button>
            {shown ? <LaunchNotes api={api} /> : null}
          </ToastsProvider>
        </QueryClientProvider>
      );
    }
    render(<Root />);
    await waitFor(() => expect(invoke).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Сменить' }));
    fireEvent.click(screen.getByRole('button', { name: 'Сменить' }));
    await act(async () => answer({ portChanged: false, restartedAfterCrash: true, updatedFrom: '1.2.0', releaseNotes: '- Карта саги показывает дороги' }));
    expect(await within(toasts()).findByText('CanonKeeper обновлён до версии 1.3.0')).toBeTruthy();
    expect(within(toasts()).getAllByText('CanonKeeper перезапущен после сбоя')).toHaveLength(1);
    expect(invoke).toHaveBeenCalledTimes(1);
  });

  it('say nothing in a browser without the shell', async () => {
    noShell();
    renderNotes();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText(/CanonKeeper перезапущен|Адрес окна|обновлён|не закончилась/u)).toBeNull();
  });
});

// specs/2026-11-15-desk-updates-web.md UW3.3 (board 44·34): after an update — «CanonKeeper обновлён до версии …» of the
// version that runs, with «Что нового» when the notes came, until closed; a package that did not finish — its own line of
// an error instead.
describe('the lines of a launch after an update', () => {
  const notes = '- Северный предел читается быстрее\n- Карта саги показывает дороги';

  it('say the app is updated to the version that runs, with «Что нового» that opens the notes, and stay until closed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0', releaseNotes: notes } });
    renderNotes('1.3.0');
    const line = (await within(toasts()).findByText('CanonKeeper обновлён до версии 1.3.0')).closest('[data-tone]') as HTMLElement;
    await act(async () => vi.advanceTimersByTimeAsync(60_000));
    expect(within(toasts()).getByText('CanonKeeper обновлён до версии 1.3.0')).toBeTruthy();
    fireEvent.click(within(line).getByRole('button', { name: 'Что нового' }));
    const window = await screen.findByRole('dialog', { name: 'Что нового в версии 1.3.0' });
    expect(within(window).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Северный предел читается быстрее', 'Карта саги показывает дороги']);
    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(within(toasts()).getByText('CanonKeeper обновлён до версии 1.3.0')).toBeTruthy();
    fireEvent.click(within(line).getByRole('button', { name: 'Закрыть уведомление' }));
    await waitFor(() => expect(within(toasts()).queryByText('CanonKeeper обновлён до версии 1.3.0')).toBeNull());
  });

  it('have no «Что нового» without the notes of the release', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0' } });
    renderNotes('1.3.0');
    const line = (await within(toasts()).findByText('CanonKeeper обновлён до версии 1.3.0')).closest('[data-tone]') as HTMLElement;
    expect(within(line).queryByRole('button', { name: 'Что нового' })).toBeNull();
  });

  // specs/2026-11-15-desk-mcp.md MC1.8: the installer stopped the bridges of the programs — the one line «обновлён» says with a
  // second sentence how they read again; no second line.
  it('say in the one line «обновлён» that the programs of the author start a new session after the installer stopped them', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0', bridgesStopped: true } });
    renderNotes('1.3.0');
    expect(await within(toasts()).findByText('CanonKeeper обновлён до версии 1.3.0. Чтобы Claude Code и Codex снова читали саги, начните в них новую сессию')).toBeTruthy();
    expect(within(toasts()).getAllByText(/^CanonKeeper обновлён/u)).toHaveLength(1);
  });

  it('say a package that did not finish with its path, and no «обновлён» beside it', async () => {
    const path = '/home/author/Загрузки/CanonKeeper_1.3.0_amd64.deb';
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0', releaseNotes: notes, updateUnfinished: { version: '1.3.0', package: path } } });
    renderNotes('1.3.0');
    const line = await within(toasts()).findByText(`Установка версии 1.3.0 не закончилась. Пакет сохранён: ${path} — установите его сами`);
    expect(line.closest('[data-tone]')!.getAttribute('data-tone')).toBe('error');
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(within(toasts()).queryByText(/обновлён/u)).toBeNull();
    expect(within(toasts()).getAllByRole('alert')).toHaveLength(1);
  });

  it('give the way to the releases when the package was not kept', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updateUnfinished: { version: '1.3.0' } } });
    renderNotes('1.2.0');
    const line = (await within(toasts()).findByText('Установка версии 1.3.0 не закончилась. Скачайте её на странице выпусков')).closest('[data-tone]') as HTMLElement;
    fireEvent.click(within(line).getByRole('link', { name: 'Открыть страницу выпусков' }));
    await waitFor(() => expect(invoke).toHaveBeenLastCalledWith('open_external', { url: 'https://github.com/canonkeeper/desktop-app/releases' }));
  });
});
