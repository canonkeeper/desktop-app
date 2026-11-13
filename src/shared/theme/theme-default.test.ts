import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { editionThemeDefault, editionThemeHtml } from '../../../vite.config';
import { DARK_DEVICE_QUERY, readSiteThemeChoice, SITE_THEME_STORAGE_KEY } from './site-theme';

const boot = readFileSync(path.join(process.cwd(), 'public/site-theme.js'), 'utf8');
const original = window.matchMedia;

function darkDevice() {
  window.matchMedia = vi.fn((query: string) => ({
    matches: query === DARK_DEVICE_QUERY, media: query, addEventListener: () => undefined, removeEventListener: () => undefined,
  })) as unknown as typeof window.matchMedia;
}

/** public/site-theme.js as the browser runs it before the app, on the page the build of an edition made. */
function runBoot(themeDefault: string | null) {
  const root = document.documentElement;
  root.removeAttribute('data-theme');
  if (themeDefault) root.setAttribute('data-theme-default', themeDefault);
  else root.removeAttribute('data-theme-default');
  new Function(boot)();
  return root.getAttribute('data-theme');
}

afterEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme-default');
  document.documentElement.removeAttribute('data-theme');
  window.matchMedia = original;
});

// specs/2026-11-13-desk-settings.md ST3.1: the edition sets the theme the author has not chosen, before the first paint —
// the app «Как в системе», the site light as before.
describe('the theme of an edition before the author chooses one', () => {
  it('is «Как в системе» in the app and nothing of its own in the site', () => {
    expect(editionThemeDefault('desktop')).toBe('system');
    expect(editionThemeDefault('site')).toBeNull();
    const page = '<!doctype html>\n<html lang="ru">\n  <head></head>\n</html>\n';
    expect(editionThemeHtml(page, 'system')).toBe('<!doctype html>\n<html lang="ru" data-theme-default="system">\n  <head></head>\n</html>\n');
    expect(editionThemeHtml(page, null)).toBe(page);
    expect(readFileSync(path.join(process.cwd(), 'index.html'), 'utf8')).toContain('<html lang="ru">');
  });

  it('turns the window of the app dark with a dark computer before the first paint, until the author chooses', () => {
    darkDevice();
    expect(runBoot('system')).toBe('dark');
    expect(readSiteThemeChoice(localStorage, document.documentElement)).toBe('system');
    localStorage.setItem(SITE_THEME_STORAGE_KEY, 'light');
    expect(runBoot('system')).toBe('light');
    expect(readSiteThemeChoice(localStorage, document.documentElement)).toBe('light');
  });

  it('keeps the site light on a dark computer', () => {
    darkDevice();
    expect(runBoot(null)).toBe('light');
    expect(readSiteThemeChoice(localStorage, document.documentElement)).toBe('light');
  });
});
