import { readFileSync } from 'node:fs';
import path from 'node:path';
import { act } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DARK_DEVICE_QUERY,
  installSiteTheme,
  readSiteThemeChoice,
  SITE_THEME_STORAGE_KEY,
  siteThemeOf,
  useSiteThemeStore,
  type SiteThemeChoice,
} from './site-theme';

/** A device whose theme the test sets and changes, as `prefers-color-scheme` does. */
function device(dark: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  const list = {
    get matches() { return dark; },
    media: DARK_DEVICE_QUERY,
    addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
  };
  window.matchMedia = vi.fn((query: string) => (query === DARK_DEVICE_QUERY ? list : { ...list, matches: false })) as unknown as typeof window.matchMedia;
  return {
    turn(next: boolean) {
      dark = next;
      for (const listener of listeners) listener({ matches: next } as MediaQueryListEvent);
    },
  };
}

const root = process.cwd();
const boot = readFileSync(path.join(root, 'public/site-theme.js'), 'utf8');

/** public/site-theme.js as the browser runs it before the app. */
function runBoot() {
  document.documentElement.removeAttribute('data-theme');
  new Function(boot)();
  return document.documentElement.getAttribute('data-theme');
}

let stop: (() => void) | null = null;
const original = window.matchMedia;

beforeEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  useSiteThemeStore.setState({ choice: 'light', theme: 'light' });
});

afterEach(() => {
  stop?.();
  stop = null;
  window.matchMedia = original;
  vi.useRealTimers();
});

describe('the theme of the site (specs/2026-11-12-ux11-dark-core.md TH1.1)', () => {
  it('is light by default, even on a dark device: the site does not turn dark on its own', () => {
    device(true);
    expect(readSiteThemeChoice()).toBe('light');
    expect(siteThemeOf('light', true)).toBe('light');
    expect(runBoot()).toBe('light');
    stop = installSiteTheme();
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
  });

  it('takes «Тёмная» from the menu: kept in the browser, on <html> at once, no colour animating meanwhile', () => {
    vi.useFakeTimers({ toFake: ['requestAnimationFrame', 'cancelAnimationFrame'] });
    device(false);
    stop = installSiteTheme();
    act(() => useSiteThemeStore.getState().choose('dark'));
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(true);
    expect(localStorage.getItem(SITE_THEME_STORAGE_KEY)).toBe('dark');
    vi.advanceTimersToNextFrame();
    vi.advanceTimersToNextFrame();
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(false);
    // After a reload the page script puts it on before the first paint.
    expect(runBoot()).toBe('dark');
  });

  it('follows the device while «Как в системе» is chosen, and changes with it without a reload', () => {
    const phone = device(false);
    stop = installSiteTheme();
    act(() => useSiteThemeStore.getState().choose('system'));
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    act(() => phone.turn(true));
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    act(() => phone.turn(false));
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    // «Светлая» chosen: the device no longer counts.
    act(() => useSiteThemeStore.getState().choose('light'));
    act(() => phone.turn(true));
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
  });

  it('takes the choice made in another tab', () => {
    device(false);
    stop = installSiteTheme();
    localStorage.setItem(SITE_THEME_STORAGE_KEY, 'dark');
    act(() => window.dispatchEvent(new StorageEvent('storage', { key: SITE_THEME_STORAGE_KEY })));
    expect(useSiteThemeStore.getState()).toMatchObject({ choice: 'dark', theme: 'dark' });
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  });

  it('reads the choice before the first paint by the same rule as the app, in every case', () => {
    for (const stored of [null, 'light', 'dark', 'system', 'sepia', '{bad'] as const) {
      for (const dark of [false, true]) {
        device(dark);
        localStorage.clear();
        if (stored !== null) localStorage.setItem(SITE_THEME_STORAGE_KEY, stored);
        const choice: SiteThemeChoice = readSiteThemeChoice();
        expect([stored, dark, runBoot()]).toEqual([stored, dark, siteThemeOf(choice, dark)]);
      }
    }
    // A closed storage leaves the site light, the app as the page script.
    expect(readSiteThemeChoice({ getItem() { throw new DOMException('Denied', 'SecurityError'); }, setItem() {} })).toBe('light');
  });

  it('is put on by a script of its own file in the head, before the app', () => {
    const page = readFileSync(path.join(root, 'index.html'), 'utf8');
    const script = page.indexOf('<script src="/site-theme.js"></script>');
    expect(script).toBeGreaterThan(0);
    expect(script).toBeLessThan(page.indexOf('</head>'));
    expect(script).toBeLessThan(page.indexOf('src="/src/main.tsx"'));
    expect(boot).toContain(`'${SITE_THEME_STORAGE_KEY}'`);
    expect(boot).toContain(`'${DARK_DEVICE_QUERY}'`);
  });
});
