// SPDX-License-Identifier: AGPL-3.0-only
import type { Edition, EditionAccount } from '../../app/edition';
import { openPages } from '../../app/pages';
import { openRoutes } from '../../routing/routes';
import type { SagaSection } from '../../ui/saga-sections';

/**
 * The sections of the desktop app by the one table of the menu (DA2.8, specs/2026-11-10-ux2-shell-core.md SC2.1): its open
 * pages and «Недавно удалённые»; «Рисование», «Галерея» and «Письмо» are of the site, a book opens from the shelf.
 */
const desktopSections: readonly SagaSection[] = ['manuscript', 'saga', 'memory', 'chat', 'notes', 'deleted'];

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
