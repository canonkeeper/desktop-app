// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { openPages } from '../../app/pages';
import { edition } from '.';

// specs/2026-11-07-desktop-app.md DA2.7, DA2.8, DA13.5, DA16.4: the desktop app has the open sections only — no sign-in,
// documents, landing, admin panel, promotion, comparison of texts or contacts, and no closed block inside a section.
describe('the desktop edition', () => {
  it('routes only the open sections', () => {
    // «Книги» has the editor of a chapter without the helper (specs/2026-11-13-desk-editor.md ED1.1) and the app has
    // «Настройки» (specs/2026-11-13-desk-settings.md ST1.2): the site has neither route.
    expect(edition.routes.map((route) => route.path)).toEqual(['/', '/chat', '/notes', '/saga', '/memory', '/manuscript/books', '/manuscript/edit', '/settings']);
    // The open pages as they are: no page of the desktop provides a closed block to its section; «Книги» gives its open
    // slots, «Настройки» are the own page of the app.
    expect(Object.keys(edition.pages).sort()).toEqual(['chat', 'manuscript', 'memory', 'notes', 'saga', 'settings']);
    for (const surface of ['chat', 'memory', 'notes', 'saga'] as const) expect(edition.pages[surface]).toBe(openPages[surface]);
    expect(edition.pages.manuscript?.preload).toBe(openPages.manuscript.preload);
  });

  it('names the open sections in its menu', () => {
    expect(edition.sections).toEqual(['manuscript', 'saga', 'memory', 'chat', 'notes', 'deleted']);
  });

  it('has no account of its own', () => {
    const account = edition.useAccount({} as Parameters<typeof edition.useAccount>[0]);
    expect(account.publicPage).toBeNull();
    expect(account.signInPage).toBeNull();
    expect(account.onLogout).toBeUndefined();
  });
});
