// SPDX-License-Identifier: AGPL-3.0-only
import { afterEach, describe, expect, it, vi } from 'vitest';
import { desktopShell } from './shell';

afterEach(() => {
  delete (window as { __TAURI__?: unknown }).__TAURI__;
});

/** The shell of the window as Tauri gives it to the page: `window.__TAURI__.core.invoke`. */
function shellOfTheWindow(answers: Record<string, unknown> = {}) {
  const invoke = vi.fn(async (command: string) => answers[command] ?? null);
  (window as { __TAURI__?: unknown }).__TAURI__ = { core: { invoke } };
  return invoke;
}

// specs/2026-11-13-desk-settings.md ST4.5: the commands of the shell (specs/2026-11-12-desk-shell.md SH3.2) — one place of
// the open web calls them, typed, and says when there is no shell.
describe('the commands of the shell of the app', () => {
  it('say there is no shell in a browser without it', () => {
    expect(desktopShell()).toBeNull();
    (window as { __TAURI__?: unknown }).__TAURI__ = {};
    expect(desktopShell()).toBeNull();
  });

  it('call the four commands of the page by their names', async () => {
    const invoke = shellOfTheWindow({ launch_notes: { portChanged: true, restartedAfterCrash: false } });
    const shell = desktopShell()!;
    await shell.openExternal('https://github.com/canonkeeper/desktop-app');
    await shell.revealDataDir();
    await expect(shell.launchNotes()).resolves.toEqual({ portChanged: true, restartedAfterCrash: false });
    await shell.closeWindow();
    expect(invoke.mock.calls).toEqual([
      ['open_external', { url: 'https://github.com/canonkeeper/desktop-app' }],
      ['reveal_data_dir', undefined],
      ['launch_notes', undefined],
      ['close_window', undefined],
    ]);
  });

  it('read nothing from an answer of launch_notes that is not its own', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: 'yes' } });
    await expect(desktopShell()!.launchNotes()).resolves.toEqual({ portChanged: false, restartedAfterCrash: false });
  });
});
