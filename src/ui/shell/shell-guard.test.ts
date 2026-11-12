import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// specs/2026-11-10-ux2-shell-core.md, Tests: one shell in the tree of a page of the author. No section renders its own
// <main>, top bar or column; the old shells are gone and nobody imports them. «Глобальный поиск» is a hidden word of the
// dictionary (src/shared/copy/dictionary.ts): its guard keeps it out of every saying.

const root = path.resolve(import.meta.dirname, '../..');

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const file = path.join(directory, name);
    if (statSync(file).isDirectory()) return name === 'generated' ? [] : sources(file);
    return /\.(ts|tsx)$/u.test(name) && !/\.test(-helpers)?\.(ts|tsx)$/u.test(name) ? [file] : [];
  });
}

const files = sources(root).map((file) => ({ file: path.relative(root, file).split(path.sep).join('/'), text: readFileSync(file, 'utf8') }));

/**
 * The pages that stand without the shell, or before the product, keep their <main>: the shell itself, sign-in, the guest
 * home, the comparison, the letter link of a permission, the admin panel, the screens of an outage and of a client error,
 * the change of the identity, and the guest «Тариф», documents and «Такой страницы нет» (S1.1).
 */
const ownMain = new Set([
  'ui/shell/Shell.tsx',
  'features/auth/AuthLayout.tsx',
  'features/landing/LandingPage.tsx',
  'features/compare/ComparePage.tsx',
  'features/promo/PromoLinkPage.tsx',
  'features/admin/AdminPage.tsx',
  'app/ServiceUnavailable.tsx',
  'app/ClientErrorScreen.tsx',
  'editions/site/account.tsx',
  'features/tiers/TariffPage.tsx',
  'features/legal/LegalDocumentPage.tsx',
  // A guest on an address of no page (specs/2026-11-11-ux9-account.md AC1.5).
  'features/auth/GuestNotFound.tsx',
]);

describe('one shell in the tree of a page', () => {
  it('leaves <main> to the shell: a page of the author renders none of its own', () => {
    expect(files.filter(({ file, text }) => /<main\b/u.test(text) && !ownMain.has(file)).map(({ file }) => file)).toEqual([]);
  });

  it('has none of the old shells, and nothing imports them', () => {
    for (const gone of ['pages/ProjectWorkspace.tsx', 'ui/ProjectSidebar.tsx', 'ui/navigation/WorkspaceTopBar.tsx',
      'features/manuscript/shelf/ShelfNavigation.tsx', 'ui/NotesButton.tsx']) {
      expect(existsSync(path.join(root, gone)), gone).toBe(false);
    }
    const names = /\b(ProjectWorkspace|ProjectSidebar|ProjectNavigationItems|WorkspaceTopBar|ShelfProjectMenu|ShelfTopBar|ReaderAppBar|ReaderMobileMenu)\b/u;
    expect(files.filter(({ text }) => names.test(text)).map(({ file }) => file)).toEqual([]);
  });

  it('marks the content of the page once, by the shell', () => {
    const marking = files.filter(({ text }) => /data-page-content=/u.test(text)).map(({ file }) => file);
    // The shell, and the pages that stand without it.
    expect(marking.every((file) => ownMain.has(file))).toBe(true);
    expect(marking).toContain('ui/shell/Shell.tsx');
  });
});
