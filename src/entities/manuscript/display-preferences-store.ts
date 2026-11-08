import { create } from 'zustand';

export const MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY = 'canonkeeper.manuscript.displayPreferences.v1';

const manuscriptDisplayPreferencesVersion = 1;

export type ReaderDisplaySize = 'normal' | 'large';
export type ReaderDisplayTheme = 'light' | 'sepia' | 'dark';

interface ReaderPreferencesSnapshot {
  displaySize: ReaderDisplaySize;
  displayTheme: ReaderDisplayTheme;
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
  setDisplaySize(displaySize: ReaderDisplaySize): void;
  setDisplayTheme(displayTheme: ReaderDisplayTheme): void;
}

const defaultReaderPreferences: ReaderPreferencesSnapshot = {
  displaySize: 'normal',
  displayTheme: 'light',
};

function browserStorage(): ReaderPreferencesStorage | null {
  if (typeof window === 'undefined') {
    return null;
  }
  return window.localStorage;
}

function isReaderDisplaySize(value: unknown): value is ReaderDisplaySize {
  return value === 'normal' || value === 'large';
}

function isReaderDisplayTheme(value: unknown): value is ReaderDisplayTheme {
  return value === 'light' || value === 'sepia' || value === 'dark';
}

function toPayload(snapshot: ReaderPreferencesSnapshot): ReaderPreferencesPayload {
  return {
    version: manuscriptDisplayPreferencesVersion,
    displaySize: snapshot.displaySize,
    displayTheme: snapshot.displayTheme,
  };
}

function parsePreferences(raw: string | null): ReaderPreferencesSnapshot | null {
  if (!raw) return null;
  try {
    const payload = JSON.parse(raw) as Partial<ReaderPreferencesPayload>;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      return null;
    }
    const payloadKeys = Object.keys(payload);
    if (
      payloadKeys.some((key) => key !== 'version' && key !== 'displaySize' && key !== 'displayTheme') ||
      payload.version !== manuscriptDisplayPreferencesVersion ||
      !isReaderDisplaySize(payload.displaySize) ||
      !isReaderDisplayTheme(payload.displayTheme)
    ) {
      return null;
    }
    return {
      displaySize: payload.displaySize,
      displayTheme: payload.displayTheme,
    };
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

export const useReaderPreferencesStore = create<ReaderPreferencesState>((set, get) => ({
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
  setDisplaySize(displaySize) {
    const snapshot = { displaySize, displayTheme: get().displayTheme };
    set(snapshot);
    writeReaderPreferences(snapshot);
  },
  setDisplayTheme(displayTheme) {
    const snapshot = { displaySize: get().displaySize, displayTheme };
    set(snapshot);
    writeReaderPreferences(snapshot);
  },
}));
