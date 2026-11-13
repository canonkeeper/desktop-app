// SPDX-License-Identifier: AGPL-3.0-only
import { lazy } from 'react';
import type { EditionPage, EditionPageProps } from '../../app/edition';
import { openPages } from '../../app/pages';
import { editAt, editorPath, newChapterAt } from '../../features/manuscript/editor/editor-address';
import { ReaderSlotProvider, type ReaderSlot } from '../../features/manuscript/reader/reader-slot';
import { ShelfSlotProvider, type ShelfSlot } from '../../features/manuscript/shelf/shelf-slot';

const importPlainEditor = () => import('../../features/manuscript/editor/PlainEditor');
const PlainEditor = lazy(() => importPlainEditor().then((module) => ({ default: module.PlainEditor })));

/** «Править главу» of the reader opens the editor of the app (specs/2026-11-13-desk-editor.md ED1.1). */
const readerSlot: ReaderSlot = { editAt };
/** «Новая глава» of the menu of a book opens it for a new chapter (ED2.1). */
const shelfSlot: ShelfSlot = { newChapterAt };

/**
 * «Книги» of the desktop app (specs/2026-11-13-desk-editor.md): the shelf and the reader of every edition, with the editor of
 * a chapter without the helper at its own address (`editorPath`) — the site writes and edits a chapter in «Письмо».
 */
function DesktopManuscriptPage(props: EditionPageProps) {
  const { api, navigate, route, user } = props;
  if (route.path === editorPath) return <PlainEditor api={api} author={user.id} navigate={navigate} route={route} />;
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
