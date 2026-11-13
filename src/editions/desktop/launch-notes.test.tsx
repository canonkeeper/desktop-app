// SPDX-License-Identifier: AGPL-3.0-only
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { noShell, shellOfTheWindow } from './settings/settings.test-helpers';

afterEach(noShell);

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

// specs/2026-11-13-desk-settings.md ST4.4 (board 44·22): once the window opens, the page asks the shell what happened and
// says it in the one area of the toasts — once a launch.
describe('the lines of a launch of the app', () => {
  it('say the app was restarted after a fall and the address of the window changed', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: true, restartedAfterCrash: true } });
    render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    expect(await screen.findByText('CanonKeeper перезапущен после сбоя')).toBeTruthy();
    expect(screen.getByText('Адрес окна сменился: место чтения и вид текста сброшены')).toBeTruthy();
    expect(invoke).toHaveBeenCalledTimes(1);
  });

  // Board 44·22: each line has its icon in the colour of a notice — a restart `rotate-ccw`, the new address `info`.
  it('mark each line by its icon in the colour of a notice', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: true, restartedAfterCrash: true } });
    render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    const restart = (await screen.findByText('CanonKeeper перезапущен после сбоя')).closest('[data-tone]')!;
    const address = screen.getByText('Адрес окна сменился: место чтения и вид текста сброшены').closest('[data-tone]')!;
    expect([restart.getAttribute('data-tone'), address.getAttribute('data-tone')]).toEqual(['info', 'info']);
    expect(restart.querySelector('svg.lucide-rotate-ccw')).not.toBeNull();
    expect(address.querySelector('svg.lucide-info')).not.toBeNull();
  });

  it('say only what happened', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: true } });
    render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    expect(await screen.findByText('CanonKeeper перезапущен после сбоя')).toBeTruthy();
    expect(screen.queryByText(/Адрес окна сменился/u)).toBeNull();
  });

  it('ask the shell once a load of the page, however often the root mounts them', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false } });
    const first = render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    first.unmount();
    render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    await vi.waitFor(() => expect(invoke).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('status')?.textContent ?? '').not.toMatch(/CanonKeeper|Адрес окна/u);
  });

  it('say nothing in a browser without the shell', async () => {
    noShell();
    render(<ToastsProvider><LaunchNotes /></ToastsProvider>);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText(/CanonKeeper перезапущен|Адрес окна/u)).toBeNull();
  });
});
