// SPDX-License-Identifier: AGPL-3.0-only
import { createElement } from 'react';
import { z } from 'zod';
import type { Edition, EditionAccount, EditionRoute } from '../../app/edition';
import { openPages } from '../../app/pages';
import { editorPath } from '../../features/manuscript/editor/editor-address';
import { openRoutes, searchValue } from '../../routing/routes';
import type { SagaSection } from '../../ui/saga-sections';
import { desktopManuscriptPage } from './DesktopManuscriptPage';
import { LaunchNotes } from './launch-notes';
import { settingsPage, settingsRoute } from './settings/route';

/**
 * The sections of the desktop app by the one table of the menu (DA2.8, specs/2026-11-10-ux2-shell-core.md SC2.1): its open
 * pages and «Недавно удалённые»; «Рисование», «Галерея» and «Письмо» are of the site, a book opens from the shelf.
 */
const desktopSections: readonly SagaSection[] = ['manuscript', 'saga', 'memory', 'chat', 'notes', 'deleted'];

/**
 * No sign-in, documents, landing or account menu (DA2.7, DA13.5): the author is the local one, known once the app opens
 * (DA3). After the pages, the lines of a launch: a restart after a fall, a new address of the window
 * (specs/2026-11-13-desk-settings.md ST4.4).
 */
const desktopAccount: EditionAccount = {
  publicPage: null,
  signInPage: null,
  wrapAccount: (product) => product,
  wrapPages: (pages) => pages,
  notices: createElement(LaunchNotes),
  overlay: null,
};

/**
 * The editor of a chapter without the helper (specs/2026-11-13-desk-editor.md ED1.1, ED2.1): a draft, or a new chapter of a
 * book until its first save. The site has no such route: it edits a chapter in «Письмо».
 */
const editorRoute: EditionRoute = {
  path: editorPath,
  validateSearch: z.object({ projectId: searchValue, draftId: searchValue, bookId: searchValue }).catch({}).parse,
};

/**
 * The desktop app (specs/2026-11-07-desktop-app.md DA16.4): only the open sections, their pages without closed blocks; «Книги»
 * with the editor of a chapter without the helper (specs/2026-11-13-desk-editor.md), and «Настройки» in the place of the
 * account menu (specs/2026-11-13-desk-settings.md ST1).
 */
export const edition: Edition = {
  routes: [...openRoutes, editorRoute, settingsRoute],
  pages: { ...openPages, manuscript: desktopManuscriptPage, settings: settingsPage },
  sections: desktopSections,
  useAccount: () => desktopAccount,
};
