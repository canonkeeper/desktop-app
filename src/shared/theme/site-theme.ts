import { create } from 'zustand';
import { withoutColorTransitions } from './switching';

/**
 * The theme of the site (specs/2026-11-12-ux11-dark-core.md TH1.1): the author's choice «Светлая», «Тёмная» or «Как в
 * системе», light by default — the site does not turn dark by the device on its own (the owner, 07.11). The choice lives
 * in this browser; `public/site-theme.js` reads the same key and puts the theme on <html> before the first paint, this
 * module keeps it there afterwards and follows the device while «Как в системе» is chosen.
 */
export type SiteThemeChoice = 'light' | 'dark' | 'system';
export type SiteTheme = 'light' | 'dark';

export const SITE_THEME_STORAGE_KEY = 'canonkeeper.site.theme.v1';
export const DARK_DEVICE_QUERY = '(prefers-color-scheme: dark)';

export const siteThemeChoices: readonly { label: string; value: SiteThemeChoice }[] = [
  { label: 'Светлая', value: 'light' },
  { label: 'Тёмная', value: 'dark' },
  { label: 'Как в системе', value: 'system' },
];

interface ThemeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function browserStorage(): ThemeStorage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
}

function isChoice(value: unknown): value is SiteThemeChoice {
  return value === 'light' || value === 'dark' || value === 'system';
}

/** The choice kept in this browser; light when there is none or the storage is closed. */
export function readSiteThemeChoice(storage: ThemeStorage | null = browserStorage()): SiteThemeChoice {
  try {
    const stored = storage?.getItem(SITE_THEME_STORAGE_KEY);
    return isChoice(stored) ? stored : 'light';
  } catch {
    return 'light';
  }
}

/** Whether the device is dark now. */
export function deviceIsDark(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(DARK_DEVICE_QUERY).matches;
}

/** The theme a choice gives: «Как в системе» is the theme of the device. */
export function siteThemeOf(choice: SiteThemeChoice, darkDevice: boolean): SiteTheme {
  return choice === 'dark' || (choice === 'system' && darkDevice) ? 'dark' : 'light';
}

interface SiteThemeState {
  choice: SiteThemeChoice;
  theme: SiteTheme;
  /** The author chooses: kept in the browser, painted at once, without colours animating. */
  choose(choice: SiteThemeChoice): void;
  /** The device changed its theme or another tab changed the choice. */
  sync(): void;
}

export const useSiteThemeStore = create<SiteThemeState>((set, get) => ({
  choice: readSiteThemeChoice(),
  theme: siteThemeOf(readSiteThemeChoice(), deviceIsDark()),
  choose(choice) {
    try {
      browserStorage()?.setItem(SITE_THEME_STORAGE_KEY, choice);
    } catch {
      // A closed storage keeps the choice for this tab.
    }
    const theme = siteThemeOf(choice, deviceIsDark());
    if (theme === get().theme) set({ choice });
    else withoutColorTransitions(() => set({ choice, theme }));
  },
  sync() {
    const choice = readSiteThemeChoice();
    const theme = siteThemeOf(choice, deviceIsDark());
    if (choice === get().choice && theme === get().theme) return;
    withoutColorTransitions(() => set({ choice, theme }));
  },
}));

/** The theme a page of its own holds <html> in while it is open, instead of the theme of the site. */
let heldTheme: SiteTheme | null = null;

/** The theme on <html>: the dark set of tokens.css stands on `data-theme="dark"`. */
export function paintSiteTheme(theme: SiteTheme, root: HTMLElement = document.documentElement) {
  const shown = heldTheme ?? theme;
  if (root.getAttribute('data-theme') !== shown) root.setAttribute('data-theme', shown);
}

/**
 * A page with a theme of its own — the comparison of texts (specs/2026-11-12-ux11-dark-sections.md DS4.2) — holds <html>
 * in it while it is open, so nothing past its edge shows another theme; `null` gives <html> back to the theme of the site.
 * The choice of the site stays as it is.
 */
export function holdSiteTheme(theme: SiteTheme | null, root: HTMLElement = document.documentElement) {
  heldTheme = theme;
  paintSiteTheme(useSiteThemeStore.getState().theme, root);
}

/**
 * Keeps <html> in the theme of the store from the start of the app: a choice, a change of the device while «Как в
 * системе» is chosen and a choice made in another tab. Returns the way to stop.
 */
export function installSiteTheme(win: Window = window) {
  const store = useSiteThemeStore;
  paintSiteTheme(store.getState().theme, win.document.documentElement);
  const stopStore = store.subscribe((state) => paintSiteTheme(state.theme, win.document.documentElement));
  const media = typeof win.matchMedia === 'function' ? win.matchMedia(DARK_DEVICE_QUERY) : null;
  const onDevice = () => store.getState().sync();
  media?.addEventListener?.('change', onDevice);
  const onStorage = (event: StorageEvent) => {
    if (event.key === SITE_THEME_STORAGE_KEY || event.key === null) store.getState().sync();
  };
  win.addEventListener('storage', onStorage);
  return () => {
    stopStore();
    media?.removeEventListener?.('change', onDevice);
    win.removeEventListener('storage', onStorage);
  };
}

/** The theme of the site and the choice, for a page that reads or changes them. */
export function useSiteTheme() {
  return useSiteThemeStore();
}
