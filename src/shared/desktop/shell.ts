// SPDX-License-Identifier: AGPL-3.0-only

/** What the shell tells the page once after the window opened (specs/2026-11-12-desk-shell.md SH1.4, SH2.2). */
export interface LaunchNotes {
  /** The remembered port was taken: the address of the window is new, its place of reading and its view are reset. */
  portChanged: boolean;
  /** The backend fell and the shell started it again. */
  restartedAfterCrash: boolean;
}

/**
 * The commands of the shell of the desktop app the page may call (specs/2026-11-12-desk-shell.md SH3.2; Tauri allows them
 * only to the exact origin of the window).
 */
export interface DesktopShell {
  /** Opens an address in the browser of the system: the shell takes only `https:` to a named host and a letter. */
  openExternal(url: string): Promise<void>;
  /** «Открыть папку»: the data directory in the file manager of the system. */
  revealDataDir(): Promise<void>;
  /** The notes of this launch; the shell gives them once, a second call says nothing new. */
  launchNotes(): Promise<LaunchNotes>;
  /** Closes the app as the button of the window does. */
  closeWindow(): Promise<void>;
}

type Invoke = (command: string, args?: Record<string, unknown>) => Promise<unknown>;

interface TauriWindow {
  __TAURI__?: { core?: { invoke?: unknown } };
}

/**
 * The shell of the window (specs/2026-11-13-desk-settings.md ST4.5): the one place of the open web that calls its commands,
 * by `window.__TAURI__` the shell puts into its window. Null in a browser without the shell — the e2e of the app,
 * `vite preview`, the site.
 */
export function desktopShell(win: Window = window): DesktopShell | null {
  const invoke = (win as Window & TauriWindow).__TAURI__?.core?.invoke;
  if (typeof invoke !== 'function') return null;
  const call = (command: string, args?: Record<string, unknown>) => (invoke as Invoke)(command, args);
  return {
    openExternal: async (url) => {
      await call('open_external', { url });
    },
    revealDataDir: async () => {
      await call('reveal_data_dir');
    },
    launchNotes: async () => notesOf(await call('launch_notes')),
    closeWindow: async () => {
      await call('close_window');
    },
  };
}

function notesOf(answer: unknown): LaunchNotes {
  const notes = typeof answer === 'object' && answer !== null ? answer as Record<string, unknown> : {};
  return { portChanged: notes.portChanged === true, restartedAfterCrash: notes.restartedAfterCrash === true };
}
