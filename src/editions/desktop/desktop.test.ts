// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { openPages } from '../../app/pages';
import { edition } from '.';

// specs/2026-11-07-desktop-app.md DA2.7, DA2.8, DA13.5, DA16.4: the desktop app has the open sections only — no sign-in,
// documents, landing, admin panel, promotion, comparison of texts or contacts, and no closed block inside a section.
describe('the desktop edition', () => {
  it('routes only the open sections', () => {
    expect(edition.routes.map((route) => route.path)).toEqual(['/', '/chat', '/notes', '/saga', '/memory', '/manuscript/books']);
    // The open pages as they are: no page of the desktop provides a closed block to its section.
    expect(edition.pages).toBe(openPages);
    expect(Object.keys(edition.pages).sort()).toEqual(['chat', 'manuscript', 'memory', 'notes', 'saga']);
  });

  it('names the open sections in its menu', () => {
    expect(edition.sections).toEqual(['manuscript', 'reader', 'chat', 'saga', 'notes']);
  });

  it('has no account of its own', () => {
    const account = edition.useAccount({} as Parameters<typeof edition.useAccount>[0]);
    expect(account.publicPage).toBeNull();
    expect(account.signInPage).toBeNull();
    expect(account.onLogout).toBeUndefined();
  });
});
