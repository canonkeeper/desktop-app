// SPDX-License-Identifier: AGPL-3.0-only
import { lazy, useMemo } from 'react';
import type { EditionPage, EditionPageProps } from '../../app/edition';
import { openPages } from '../../app/pages';
import { editAt, editorPath, newChapterAt } from '../../features/manuscript/editor/editor-address';
import { ReaderSlotProvider, type ReaderSlot } from '../../features/manuscript/reader/reader-slot';
import { ShelfSlotProvider } from '../../features/manuscript/shelf/shelf-slot';
import { obsidianPath } from './obsidian/address';
import { ObsidianPageAt, obsidianShelf } from './obsidian/entries';
import { obsidianBookMenu } from './obsidian/update-source';
import { OpenPlace, openPlacePath } from './open-place/OpenPlace';
import { sagaFileOfferSlot } from './saga-file/SagaFileCard';

const importPlainEditor = () => import('../../features/manuscript/editor/PlainEditor');
const PlainEditor = lazy(() => importPlainEditor().then((module) => ({ default: module.PlainEditor })));

/** «Править главу» of the reader opens the editor of the app (specs/2026-11-13-desk-editor.md ED1.1). */
const readerSlot: ReaderSlot = { editAt };

/**
 * «Книги» of the desktop app (specs/2026-11-13-desk-editor.md): the shelf and the reader of every edition, with the editor of
 * a chapter without the helper at its own address (`editorPath`) — the site writes and edits a chapter in «Письмо» — and the
 * page of the bridge with Obsidian at its own (`obsidianPath`, specs/2026-11-15-desk-obsidian-web.md OW1.1), and the page a
 * link `canonkeeper://` opens at its own (`openPlacePath`, specs/2026-11-16-desk-app-links.md AL1.6).
 */
function DesktopManuscriptPage(props: EditionPageProps) {
  const { api, navigate, route, user } = props;
  // «Новая глава» of the menu of a book opens it for a new chapter (ED2.1); the first screen opens a saga file
  // (specs/2026-11-13-desk-sagafile-web.md SW2.1); «Из Obsidian…» beside «Добавить книги» and the volumes of a load from an
  // Obsidian folder (specs/2026-11-15-desk-obsidian-web.md OW1.2, OW4.6), the book and its folder in the menu of a book
  // (specs/2026-11-15-desk-obsidian-book-web.md OU1).
  const shelfSlot = useMemo(() => ({
    newChapterAt, useSagaFileOffer: sagaFileOfferSlot(api), ...obsidianShelf, useBookMenu: obsidianBookMenu(api),
  }), [api]);
  if (route.path === editorPath) return <PlainEditor api={api} author={user.id} navigate={navigate} route={route} />;
  if (route.path === obsidianPath) return <ObsidianPageAt {...props} />;
  if (route.path === openPlacePath) return <OpenPlace api={api} navigate={navigate} route={route} />;
  return (
    <ReaderSlotProvider value={readerSlot}>
      <ShelfSlotProvider value={shelfSlot}>
        {openPages.manuscript.render(props)}
      </ShelfSlotProvider>
    </ReaderSlotProvider>
  );
}

export const desktopManuscriptPage: EditionPage = {
  render: (props) => <DesktopManuscriptPage {...props} />,
  preload: openPages.manuscript.preload,
};
