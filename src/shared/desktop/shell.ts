// SPDX-License-Identifier: AGPL-3.0-only

/** What the shell tells the page once after the window opened (specs/2026-11-12-desk-shell.md SH1.4, SH2.2). */
export interface LaunchNotes {
  /** The remembered port was taken: the address of the window is new, its place of reading and its view are reset. */
  portChanged: boolean;
  /** The backend fell and the shell started it again. */
  restartedAfterCrash: boolean;
  /**
   * The version the data were opened with before this one: the app was updated (specs/2026-11-13-desk-upgrade.md UG2.5;
   * specs/2026-11-15-desk-updates-web.md UW3.3).
   */
  updatedFrom?: string;
  /** The notes of the release now running, when the app installed it (specs/2026-11-13-desk-updates.md UB2.8). */
  releaseNotes?: string;
  /** A package of Linux the system did not finish installing: its version and where it is kept, when it could be (UB2.6). */
  updateUnfinished?: { version: string; package?: string };
  /**
   * The installer stopped the bridges of the programs of the author for this update (specs/2026-11-15-desk-mcp.md MC1.8): the
   * line «обновлён» says how they read the sagas again — once, and only in the launch updated to that version.
   */
  bridgesStopped?: true;
}

/** The reasons the shell or the backend refuses a folder chosen as the vault of a saga (specs/2026-11-13-desk-obsidian.md OB1.3). */
export type FolderRefusal = 'data_dir' | 'too_wide' | 'not_found' | 'not_folder' | 'unreadable';

/**
 * How a choice of the folder of an Obsidian vault ended (OB1.2): the folder is the vault of the saga, it was refused with its
 * reason, the author canceled, or the choice went by. The path never comes to the page.
 */
export type FolderChoice =
  | { outcome: 'linked' }
  | { outcome: 'refused'; reason: FolderRefusal }
  | { outcome: 'canceled' }
  | { outcome: 'expired' };

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
  /**
   * Opens the system window that chooses the folder of the Obsidian vault of a saga for the number beginObsidianVaultChoice
   * answered, and says how the choice ended once the backend checked the folder (OB1.2).
   */
  pickFolder(request: string): Promise<FolderChoice>;
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
    pickFolder: async (request) => folderChoiceOf(await call('pick_folder', { request })),
  };
}

const refusals: readonly FolderRefusal[] = ['data_dir', 'too_wide', 'not_found', 'not_folder', 'unreadable'];

/** The outcome the shell answered; anything else is a choice that went by. */
function folderChoiceOf(answer: unknown): FolderChoice {
  const said = typeof answer === 'object' && answer !== null ? answer as Record<string, unknown> : {};
  switch (said.outcome) {
    case 'linked':
    case 'canceled':
      return { outcome: said.outcome };
    case 'refused':
      return refusals.includes(said.reason as FolderRefusal)
        ? { outcome: 'refused', reason: said.reason as FolderRefusal }
        : { outcome: 'expired' };
    default:
      return { outcome: 'expired' };
  }
}

function notesOf(answer: unknown): LaunchNotes {
  const notes = typeof answer === 'object' && answer !== null ? answer as Record<string, unknown> : {};
  const text = (value: unknown) => (typeof value === 'string' && value !== '' ? value : undefined);
  const unfinished = typeof notes.updateUnfinished === 'object' && notes.updateUnfinished !== null
    ? notes.updateUnfinished as Record<string, unknown>
    : null;
  const version = text(unfinished?.version);
  const kept = text(unfinished?.package);
  const updatedFrom = text(notes.updatedFrom);
  const releaseNotes = text(notes.releaseNotes);
  return {
    portChanged: notes.portChanged === true,
    restartedAfterCrash: notes.restartedAfterCrash === true,
    ...(updatedFrom ? { updatedFrom } : {}),
    ...(releaseNotes ? { releaseNotes } : {}),
    ...(version ? { updateUnfinished: { version, ...(kept ? { package: kept } : {}) } } : {}),
    ...(notes.bridgesStopped === true ? { bridgesStopped: true as const } : {}),
  };
}
