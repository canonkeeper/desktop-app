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

  // specs/2026-11-13-desk-obsidian.md OB1.2: the fifth command opens the system window for the number of a choice and gives
  // the page only how the choice ended.
  it('call pick_folder with the number of the choice and read only its outcome', async () => {
    const invoke = shellOfTheWindow({ pick_folder: { outcome: 'refused', reason: 'data_dir', path: '/home/author' } });
    const shell = desktopShell()!;
    await expect(shell.pickFolder('Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTAwMDA')).resolves.toEqual({ outcome: 'refused', reason: 'data_dir' });
    expect(invoke.mock.calls).toEqual([['pick_folder', { request: 'Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTAwMDA' }]]);
    for (const [answer, choice] of [
      [{ outcome: 'linked' }, { outcome: 'linked' }],
      [{ outcome: 'canceled' }, { outcome: 'canceled' }],
      [{ outcome: 'refused', reason: 'elsewhere' }, { outcome: 'expired' }],
      [null, { outcome: 'expired' }],
    ] as const) {
      shellOfTheWindow({ pick_folder: answer });
      await expect(desktopShell()!.pickFolder('n')).resolves.toEqual(choice);
    }
  });

  it('read nothing from an answer of launch_notes that is not its own', async () => {
    shellOfTheWindow({ launch_notes: { portChanged: 'yes', updatedFrom: 12, releaseNotes: '', updateUnfinished: { package: '/x.deb' } } });
    await expect(desktopShell()!.launchNotes()).resolves.toEqual({ portChanged: false, restartedAfterCrash: false });
  });

  // specs/2026-11-15-desk-updates-web.md UW3.3: the notes of an update the shell gives (crates/core/src/supervisor.rs, Notes)
  // reach the page — the version before, the notes of the release, the package of Linux that did not finish.
  it('give the notes of an update of the launch', async () => {
    shellOfTheWindow({
      launch_notes: {
        portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0', releaseNotes: '- Карта саги показывает дороги',
        updateUnfinished: { version: '1.3.0', package: '/home/author/Загрузки/CanonKeeper_1.3.0_amd64.deb' },
      },
    });
    await expect(desktopShell()!.launchNotes()).resolves.toEqual({
      portChanged: false, restartedAfterCrash: false, updatedFrom: '1.2.0', releaseNotes: '- Карта саги показывает дороги',
      updateUnfinished: { version: '1.3.0', package: '/home/author/Загрузки/CanonKeeper_1.3.0_amd64.deb' },
    });
    shellOfTheWindow({ launch_notes: { portChanged: false, restartedAfterCrash: false, updateUnfinished: { version: '1.3.0' } } });
    await expect(desktopShell()!.launchNotes()).resolves.toEqual({ portChanged: false, restartedAfterCrash: false, updateUnfinished: { version: '1.3.0' } });
  });
});
