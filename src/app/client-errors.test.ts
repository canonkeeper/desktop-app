import { describe, expect, it, vi } from 'vitest';
import {
  createClientErrorReporter,
  installClientErrorReporting,
  isChunkLoadError,
  shouldReloadAfterChunkFailure,
  type ClientErrorEnvironment,
} from './client-errors';

const environment: ClientErrorEnvironment = {
  origin: 'https://canonkeeper.ru',
  pathname: '/chat',
  entryScript: 'https://canonkeeper.ru/assets/index-Bq3x9kLm.js',
  userAgent: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36',
};

function errorWithStack(name: string, message: string, stack: string) {
  const error = new Error(message);
  error.name = name;
  error.stack = stack;
  return error;
}

describe('client error reports', () => {
  it('sends only the kind, class, surface, build, bundle frames and browser class (R5.3)', () => {
    const reportClientError = vi.fn().mockResolvedValue(undefined);
    const reporter = createClientErrorReporter({ reportClientError }, () => environment);
    reporter.report('error', errorWithStack('TypeError', 'Cannot read «Глава 7. Тайна» of undefined', [
      'TypeError: Cannot read «Глава 7. Тайна» of undefined',
      '    at render (https://canonkeeper.ru/assets/ChatPage-D4f2.js:1:2345)',
      '    at https://canonkeeper.ru/assets/index-Bq3x9kLm.js:12:99',
      '    at evil (https://cdn.example/lib.js:1:1)',
      '    at eval (eval at run (https://canonkeeper.ru/assets/index-Bq3x9kLm.js:1:1), <anonymous>:1:1)',
    ].join('\n')));

    expect(reportClientError).toHaveBeenCalledTimes(1);
    const [report] = reportClientError.mock.calls[0]!;
    expect(report).toEqual({
      kind: 'error',
      name: 'TypeError',
      surface: 'chat',
      build: 'index-Bq3x9kLm.js',
      frames: ['ChatPage-D4f2.js:1:2345', 'index-Bq3x9kLm.js:12:99'],
      browser: 'chrome',
      mobile: false,
    });
    expect(JSON.stringify(report)).not.toContain('Глава');
  });

  it('names the guest home page as its own surface', () => {
    const reportClientError = vi.fn().mockResolvedValue(undefined);
    const reporter = createClientErrorReporter({ reportClientError }, () => ({ ...environment, pathname: '/' }));
    reporter.report('error', new TypeError('home'));

    expect(reportClientError.mock.calls[0]![0]).toMatchObject({ surface: 'home' });
  });

  // specs/2026-11-10-ux6-drawings-section.md DS2.2: «Галерея» and «Иллюстрации» left for «Рисунки»; their addresses only lead there.
  it('names «Рисунки» as their surface and the former addresses as no page', () => {
    for (const [pathname, surface] of [['/drawing', 'drawing'], ['/gallery', 'unknown'], ['/illustrations', 'unknown']] as const) {
      const reportClientError = vi.fn().mockResolvedValue(undefined);
      createClientErrorReporter({ reportClientError }, () => ({ ...environment, pathname })).report('error', new TypeError(pathname));
      expect(reportClientError.mock.calls[0]![0]).toMatchObject({ surface });
    }
  });

  it('names the pages of promotion permissions as their own surfaces', () => {
    const reportClientError = vi.fn().mockResolvedValue(undefined);
    for (const pathname of ['/promo-permissions', '/promo/link', '/compare/any-slug-of-a-test']) {
      createClientErrorReporter({ reportClientError }, () => ({ ...environment, pathname })).report('error', new TypeError(pathname));
    }

    expect(reportClientError.mock.calls.map(([report]) => report.surface)).toEqual(['promo', 'promo-link', 'compare']);
  });

  it('does not repeat a report and stops after five per page (R5.3)', () => {
    const reportClientError = vi.fn().mockResolvedValue(undefined);
    const reporter = createClientErrorReporter({ reportClientError }, () => environment);
    const same = new TypeError('first');
    reporter.report('error', same);
    reporter.report('error', same);
    for (const name of ['RangeError', 'SyntaxError', 'URIError', 'EvalError', 'ReferenceError', 'AggregateError']) {
      reporter.report('rejection', errorWithStack(name, 'x', ''));
    }
    expect(reportClientError).toHaveBeenCalledTimes(5);
  });

  it('reports values that are not errors without their content', () => {
    const reportClientError = vi.fn().mockResolvedValue(undefined);
    createClientErrorReporter({ reportClientError }, () => environment).report('rejection', 'секретный текст главы');
    expect(reportClientError.mock.calls[0]![0]).toMatchObject({ kind: 'rejection', name: 'NonError' });
    expect(JSON.stringify(reportClientError.mock.calls[0]![0])).not.toContain('секрет');
  });

  it('swallows a failed report so a broken network does not loop', async () => {
    const reportClientError = vi.fn().mockRejectedValue(new Error('offline'));
    createClientErrorReporter({ reportClientError }, () => environment).report('error', new Error('x'));
    await Promise.resolve();
    expect(reportClientError).toHaveBeenCalledTimes(1);
  });

  it('reports unhandled errors and rejections, and a failed chunk as chunk', () => {
    const report = vi.fn();
    const target = new EventTarget();
    const uninstall = installClientErrorReporting({ report }, target);
    const thrown = new TypeError('boom');
    target.dispatchEvent(Object.assign(new Event('error'), { error: thrown }));
    const chunk = new TypeError('Failed to fetch dynamically imported module: https://canonkeeper.ru/assets/ChatPage-old.js');
    target.dispatchEvent(Object.assign(new Event('unhandledrejection'), { reason: chunk }));
    uninstall();
    target.dispatchEvent(Object.assign(new Event('error'), { error: thrown }));

    expect(report.mock.calls).toEqual([['error', thrown], ['chunk', chunk]]);
  });

  it('recognizes chunk load failures of the supported browsers', () => {
    expect(isChunkLoadError(new TypeError('Failed to fetch dynamically imported module: /assets/a.js'))).toBe(true);
    expect(isChunkLoadError(new TypeError('error loading dynamically imported module: /assets/a.js'))).toBe(true);
    expect(isChunkLoadError(new TypeError('Importing a module script failed.'))).toBe(true);
    expect(isChunkLoadError(new Error('Unable to preload CSS for /assets/a.css'))).toBe(true);
    expect(isChunkLoadError(new TypeError('x is undefined'))).toBe(false);
    expect(isChunkLoadError('Failed to fetch dynamically imported module')).toBe(false);
  });

  it('reloads after a chunk failure only once a minute (R5.2)', () => {
    const storage = new Map<string, string>();
    const session = { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => void storage.set(key, value) };
    expect(shouldReloadAfterChunkFailure(session, 1_000_000)).toBe(true);
    expect(shouldReloadAfterChunkFailure(session, 1_030_000)).toBe(false);
    expect(shouldReloadAfterChunkFailure(session, 1_061_000)).toBe(true);
    const broken = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); } };
    expect(shouldReloadAfterChunkFailure(broken, 1_000_000)).toBe(false);
  });
});
