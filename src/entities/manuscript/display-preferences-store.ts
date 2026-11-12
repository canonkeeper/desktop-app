import { create } from 'zustand';
import { useSiteThemeStore } from '../../shared/theme/site-theme';
import { withoutColorTransitions } from '../../shared/theme/switching';

export const MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY = 'canonkeeper.manuscript.displayPreferences.v1';

const manuscriptDisplayPreferencesVersion = 3;

/** The sizes of the text of the reader and «Письмо», «А− А+» (specs/2026-11-10-ux7-reader.md RD1.4). */
export const readerTextSizes = [16, 18, 20, 23, 26, 29, 32] as const;
export type ReaderTextSize = typeof readerTextSizes[number];
/**
 * «Фон текста» (specs/2026-11-12-ux11-dark-core.md TH1.3): the background of the sheet of text of the reader and of
 * «Письмо». Until the author chooses one, the sheet follows the theme of the site (`null`); a chosen one holds on any site.
 */
export type ReaderDisplayTheme = 'light' | 'sepia' | 'dark';
/** The width of the column of the text: «Узкая · Обычная (680 px) · Широкая». */
export type ReaderColumnWidth = 'narrow' | 'normal' | 'wide';

export const readerColumnWidths: Readonly<Record<ReaderColumnWidth, number>> = { narrow: 560, normal: 680, wide: 820 };

interface ReaderPreferencesSnapshot {
  textSize: ReaderTextSize;
  /** The background the author chose; `null` — none, the sheet follows the site. */
  displayTheme: ReaderDisplayTheme | null;
  columnWidth: ReaderColumnWidth;
}

interface ReaderPreferencesPayload extends ReaderPreferencesSnapshot {
  version: typeof manuscriptDisplayPreferencesVersion;
}

interface ReaderPreferencesStorage {
  getItem(key: string): string | null;
  removeItem(key: string): void;
  setItem(key: string, value: string): void;
}

interface ReaderPreferencesState extends ReaderPreferencesSnapshot {
  hydrateReaderPreferences(storage?: ReaderPreferencesStorage | null): void;
  resetReaderPreferences(storage?: ReaderPreferencesStorage | null): void;
  /** One step of «А− А+»; nothing past the ends. */
  stepTextSize(step: -1 | 1): void;
  setDisplayTheme(displayTheme: ReaderDisplayTheme): void;
  setColumnWidth(columnWidth: ReaderColumnWidth): void;
}

const defaultReaderPreferences: ReaderPreferencesSnapshot = {
  textSize: 18,
  displayTheme: null,
  columnWidth: 'normal',
};

/** The two sizes before the steps go to their nearest step: «Обычный» was 18 px, «Крупный» 20 px. */
const formerSizes: Readonly<Record<string, ReaderTextSize>> = { normal: 18, large: 20 };

function browserStorage(): ReaderPreferencesStorage | null {
  if (typeof window === 'undefined') {
    return null;
  }
  return window.localStorage;
}

function isTextSize(value: unknown): value is ReaderTextSize {
  return readerTextSizes.some((size) => size === value);
}

function isReaderDisplayTheme(value: unknown): value is ReaderDisplayTheme {
  return value === 'light' || value === 'sepia' || value === 'dark';
}

function isColumnWidth(value: unknown): value is ReaderColumnWidth {
  return value === 'narrow' || value === 'normal' || value === 'wide';
}

function toPayload(snapshot: ReaderPreferencesSnapshot): ReaderPreferencesPayload {
  return {
    version: manuscriptDisplayPreferencesVersion,
    textSize: snapshot.textSize,
    displayTheme: snapshot.displayTheme,
    columnWidth: snapshot.columnWidth,
  };
}

/**
 * Before the theme of the site the background was always kept, «Светлая» by default: a light one of then is no choice
 * the author made against a dark site — the sheet follows the site; sepia and dark stay chosen.
 */
const formerBackground = (theme: ReaderDisplayTheme) => (theme === 'light' ? null : theme);

function parsePreferences(raw: string | null): ReaderPreferencesSnapshot | null {
  if (!raw) return null;
  try {
    const payload = JSON.parse(raw) as Record<string, unknown> | null;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
    const keys = Object.keys(payload);
    if (payload.version === manuscriptDisplayPreferencesVersion) {
      if (
        keys.some((key) => !['version', 'textSize', 'displayTheme', 'columnWidth'].includes(key))
        || !(payload.displayTheme === null || isReaderDisplayTheme(payload.displayTheme))
        || !isTextSize(payload.textSize)
        || !isColumnWidth(payload.columnWidth)
      ) {
        return null;
      }
      return { textSize: payload.textSize, displayTheme: payload.displayTheme, columnWidth: payload.columnWidth };
    }
    if (!isReaderDisplayTheme(payload.displayTheme)) return null;
    // The first version kept two sizes; they become their nearest step and the column its usual width.
    if (payload.version === 1) {
      const textSize = typeof payload.displaySize === 'string' ? formerSizes[payload.displaySize] : undefined;
      if (!textSize || keys.some((key) => key !== 'version' && key !== 'displaySize' && key !== 'displayTheme')) return null;
      return { textSize, displayTheme: formerBackground(payload.displayTheme), columnWidth: 'normal' };
    }
    if (
      payload.version !== 2
      || keys.some((key) => !['version', 'textSize', 'displayTheme', 'columnWidth'].includes(key))
      || !isTextSize(payload.textSize)
      || !isColumnWidth(payload.columnWidth)
    ) {
      return null;
    }
    return { textSize: payload.textSize, displayTheme: formerBackground(payload.displayTheme), columnWidth: payload.columnWidth };
  } catch {
    return null;
  }
}

export function readReaderPreferences(storage = browserStorage()): ReaderPreferencesSnapshot {
  if (!storage) {
    return defaultReaderPreferences;
  }
  try {
    return parsePreferences(storage.getItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY)) ?? defaultReaderPreferences;
  } catch {
    return defaultReaderPreferences;
  }
}

function writeReaderPreferences(snapshot: ReaderPreferencesSnapshot, storage = browserStorage()) {
  if (!storage) {
    return;
  }
  try {
    storage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, JSON.stringify(toPayload(snapshot)));
  } catch {
    // В приватном режиме и встроенном браузере доступ к storage может быть запрещён; настройки остаются в памяти.
  }
}

/** «3 из 7»: the place of a size among the steps, from one. */
export function textSizeStep(size: ReaderTextSize) {
  return readerTextSizes.indexOf(size) + 1;
}

export const useReaderPreferencesStore = create<ReaderPreferencesState>((set, get) => {
  const change = (patch: Partial<ReaderPreferencesSnapshot>) => {
    const { textSize, displayTheme, columnWidth } = get();
    const snapshot = { textSize, displayTheme, columnWidth, ...patch };
    set(snapshot);
    writeReaderPreferences(snapshot);
  };
  return {
    ...readReaderPreferences(),
    hydrateReaderPreferences(storage) {
      set(readReaderPreferences(storage ?? browserStorage()));
    },
    resetReaderPreferences(storage) {
      set(defaultReaderPreferences);
      try {
        const targetStorage = storage ?? browserStorage();
        targetStorage?.removeItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY);
      } catch {
        // Ошибка очистки storage не должна мешать запуску читалки.
      }
    },
    stepTextSize(step) {
      const next = readerTextSizes[textSizeStep(get().textSize) - 1 + step];
      if (next) change({ textSize: next });
    },
    setDisplayTheme(displayTheme) {
      // The new background stands in one frame, nothing animates its colours (TH3.3).
      withoutColorTransitions(() => change({ displayTheme }));
    },
    setColumnWidth(columnWidth) {
      change({ columnWidth });
    },
  };
});

/** The background of the text as it stands: the chosen one, or the theme of the site while none is chosen (TH1.3). */
export function textBackgroundOf(chosen: ReaderDisplayTheme | null, site: 'light' | 'dark'): ReaderDisplayTheme {
  return chosen ?? site;
}

/** The background of the sheet of text now: the reader and «Письмо» paint it, the windows of the sheet follow it (TH1.4). */
export function useTextBackground(): ReaderDisplayTheme {
  const chosen = useReaderPreferencesStore((state) => state.displayTheme);
  const site = useSiteThemeStore((state) => state.theme);
  return textBackgroundOf(chosen, site);
}
