// SPDX-License-Identifier: AGPL-3.0-only
import { createElement, Fragment } from 'react';
import { z } from 'zod';
import type { Edition, EditionAccount, EditionAccountInput, EditionRoute } from '../../app/edition';
import { openPages } from '../../app/pages';
import { PreparationViewProvider } from '../../entities/manuscript/preparation-slot';
import { editorPath } from '../../features/manuscript/editor/editor-address';
import { openRoutes, searchValue } from '../../routing/routes';
import { TopBarTierProvider } from '../../ui/navigation/TopBarStrip';
import { AccountLinkProvider } from './account/AccountLink';
import { TierMark } from './account/TierMark';
import type { SagaSection } from '../../ui/saga-sections';
import { desktopManuscriptPage } from './DesktopManuscriptPage';
import { LaunchNotes } from './launch-notes';
import { obsidianPath } from './obsidian/address';
import { desktopMemoryPage } from './obsidian/entries';
import { LoadOutcome } from './obsidian/LoadOutcome';
import { AppBookUpdateWords } from './obsidian/update-words';
import { ReadingClientProvider } from './reading/reading-api';
import { useDesktopReadingView } from './reading-view';
import { SagaOpened } from './saga-file/SagaOpened';
import { settingsPage, settingsRoute } from './settings/route';
import { AppUpdates } from './updates/AppUpdates';
import { AfterWelcome } from './updates/welcome-state';
import { Welcome } from './updates/Welcome';

/**
 * The sections of the desktop app by the one table of the menu (DA2.8, specs/2026-11-10-ux2-shell-core.md SC2.1): its open
 * pages and «Недавно удалённые»; «Рисование», «Галерея» and «Письмо» are of the site, a book opens from the shelf.
 */
const desktopSections: readonly SagaSection[] = ['manuscript', 'saga', 'memory', 'chat', 'notes', 'deleted'];

/**
 * No sign-in, documents, landing or account menu (DA2.7, DA13.5): the author is the local one, known once the app opens
 * (DA3). After the pages, the lines of a launch: a restart after a fall, a new address of the window
 * (specs/2026-11-13-desk-settings.md ST4.4), and the window «Сага открыта» of a saga file opened from any page
 * (specs/2026-11-13-desk-sagafile-web.md SW2.3), after «Добро пожаловать» over the whole product (specs/2026-11-15-desk-updates-web.md
 * UW1). Over the pages, the line of the reading with its buttons and windows
 * (specs/2026-11-13-desk-helper-web.md HW3.1, specs/2026-11-13-desk-consent-web.md RW1.2) and the client its writes go by,
 * and the one slot of the chat of the app: its words for a failed turn (specs/2026-11-15-desk-interrupted-turn.md IT1.4) and
 * the reason of «Спросить» while an update is installed (UW3.4).
 */
function desktopAccount({ api, navigate, route }: EditionAccountInput): EditionAccount {
  return {
    publicPage: null,
    signInPage: null,
    wrapAccount: (product) => product,
    // The update of the app: its strip under the top bar and the slot of the chat of the app — the words of a failed turn and
    // the reason of «Спросить» (specs/2026-11-15-desk-updates-web.md UW3; specs/2026-11-15-desk-interrupted-turn.md IT1.4).
    // The link with an account over the pages: its one start, the window of the waiting and the observer of its end
    // (specs/2026-11-15-desk-account-link-web.md AW1), and the tier of the linked app in the top bar
    // (specs/2026-11-15-desk-account-words-web.md AW4.7). The words of the update of a book of the app over the pages and the
    // shell (specs/2026-11-15-desk-obsidian-book-web.md OU3.1).
    wrapPages: (pages) => createElement(AppUpdates, { api, navigate, route }, createElement(AccountLinkProvider, { api, route }, createElement(
      TopBarTierProvider, { value: createElement(TierMark, { navigate }) }, createElement(
        ReadingClientProvider, { value: api }, createElement(PreparationViewProvider, { value: useDesktopReadingView }, createElement(
          AppBookUpdateWords, { api }, pages,
        )),
      ),
    ))),
    // The lines and windows that open by themselves wait for «Добро пожаловать» (specs/2026-11-15-desk-updates-web.md UW1.4).
    // The end of a load from an Obsidian folder on another page, and the read of the folder of the saga once its books are in
    // (specs/2026-11-15-desk-obsidian-web.md OW4.1, OW4.5).
    notices: createElement(Fragment, null,
      createElement(AfterWelcome, { api }, createElement(LaunchNotes, { api }), createElement(SagaOpened, { navigate })),
      createElement(LoadOutcome, { api, navigate, route })),
    overlay: createElement(Welcome, { api }),
  };
}

/**
 * The editor of a chapter without the helper (specs/2026-11-13-desk-editor.md ED1.1, ED2.1): a draft, or a new chapter of a
 * book until its first save. The site has no such route: it edits a chapter in «Письмо».
 */
const editorRoute: EditionRoute = {
  path: editorPath,
  validateSearch: z.object({ projectId: searchValue, draftId: searchValue, bookId: searchValue }).catch({}).parse,
};

/**
 * The page of the bridge with Obsidian of a saga (specs/2026-11-15-desk-obsidian-web.md OW1.1): «Книги и записи из Obsidian», its
 * step in the address. The site has no such route.
 */
const obsidianRoute: EditionRoute = {
  path: obsidianPath,
  validateSearch: z.object({ projectId: searchValue, step: z.enum(['preview']).optional().catch(undefined) }).catch({}).parse,
};

/**
 * The desktop app (specs/2026-11-07-desktop-app.md DA16.4): only the open sections, their pages without closed blocks; «Книги»
 * with the editor of a chapter without the helper (specs/2026-11-13-desk-editor.md) and the page of the bridge with Obsidian,
 * «Ваши материалы» with its entry (specs/2026-11-15-desk-obsidian-web.md), and «Настройки» in the place of the account menu
 * (specs/2026-11-13-desk-settings.md ST1).
 */
export const edition: Edition = {
  routes: [...openRoutes, editorRoute, obsidianRoute, settingsRoute],
  pages: { ...openPages, manuscript: desktopManuscriptPage, memory: desktopMemoryPage, settings: settingsPage },
  sections: desktopSections,
  useAccount: desktopAccount,
};
