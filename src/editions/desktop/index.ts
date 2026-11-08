// SPDX-License-Identifier: AGPL-3.0-only
import type { Edition, EditionAccount } from '../../app/edition';
import { openPages } from '../../app/pages';
import { openRoutes } from '../../routing/routes';
import type { SagaSection } from '../../ui/saga-sections';

/** The sections of the desktop app (DA2.8); «Что помнит помощник» and «Материалы автора» open from the saga map. */
const desktopSections: readonly SagaSection[] = ['manuscript', 'reader', 'chat', 'saga', 'notes'];

/**
 * No sign-in, documents, landing or account menu (DA2.7, DA13.5): the author is the local one, known once the app opens
 * (DA3).
 */
const desktopAccount: EditionAccount = {
  publicPage: null,
  signInPage: null,
  wrapAccount: (product) => product,
  wrapPages: (pages) => pages,
  notices: null,
  overlay: null,
};

/** The desktop app (specs/2026-11-07-desktop-app.md DA16.4): only the open sections, their pages without closed blocks. */
export const edition: Edition = {
  routes: openRoutes,
  pages: openPages,
  sections: desktopSections,
  useAccount: () => desktopAccount,
};
