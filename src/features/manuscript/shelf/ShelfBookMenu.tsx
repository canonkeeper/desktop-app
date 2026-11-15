import { useRef } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { emailUnverifiedShort } from '../../../shared/session/account-session';
import { usePresence } from '../../../ui/overlays/use-presence';
import { BookActionMenu } from './BookActionMenu';
import type { useBookFileUpdate } from './BookFileUpdate';
import { updateLine } from './book-revert-words';
import { useBookUpdateWords } from './book-update-words';
import { noBookMenu, useShelfSlot } from './shelf-slot';
import type { useBookMove } from './useBookMove';
import type { useShelfBookActions } from './useShelfBookActions';

interface ShelfBookMenuProps {
  actions: ReturnType<typeof useShelfBookActions>;
  canEdit: boolean;
  demo: boolean;
  emailUnverified: boolean;
  move: ReturnType<typeof useBookMove>;
  navigate: RouteNavigate;
  onChanges: (book: Book) => void;
  onDownload: (book: Book) => void;
  onSummary: (book: Book) => void;
  projectId: string;
  update: ReturnType<typeof useBookFileUpdate>;
  /** The load of the saga: any work on its books, a live load of «Книги», files sent now, an update of a book in work. */
  upload: { running: boolean; live: boolean; sending: boolean; updating: boolean };
}

/**
 * The menu of a book of «Книги» (specs/2026-11-11-ux7-shelf.md SF1.4): its items with why they wait now
 * (specs/2026-11-10-ux2-patterns-refusals.md PR1.1) in the words of the edition (specs/2026-11-15-desk-obsidian-book-web.md OU3.2),
 * and the items of the edition after «Новая версия из файла…» with the windows they open (OU1).
 */
export function ShelfBookMenu({ actions, canEdit, demo, emailUnverified, move, navigate, onChanges, onDownload, onSummary, projectId, update, upload }: ShelfBookMenuProps) {
  const shelfSlot = useShelfSlot();
  const words = useBookUpdateWords();
  const presence = usePresence(actions.menuBook);
  const lastAnchor = useRef(actions.menuAnchor);
  if (actions.menuAnchor) lastAnchor.current = actions.menuAnchor;
  const useBookMenu = shelfSlot?.useBookMenu ?? noBookMenu;
  const edition = useBookMenu({
    projectId, canEdit: canEdit && !demo, navigate, open: update.open, updating: (book) => actions.isBookUpdating(book.id),
    busy: update.busy || upload.updating, uploading: upload.live || upload.sending,
  });
  // Why «Новая версия из файла…» and «Удалить книгу…» of a book wait now.
  const updateReasonOf = (book: Book) => actions.isBookUpdating(book.id) ? words.updating.already
    : emailUnverified ? emailUnverifiedShort
      : upload.running || update.busy ? 'Пока идёт загрузка книг — новую версию можно будет выбрать после неё.' : null;
  // UP3.5: while books are uploaded no book of the saga is deleted — the server holds the places of the volumes.
  const deleteReasonOf = (book: Book) => actions.isBookUpdating(book.id) ? words.updating.noDelete
    : upload.live ? 'Пока идёт загрузка книг, удалить книгу нельзя — дождитесь её конца.' : null;
  /** An item that leaves the menu for what it opens. */
  const then = (act: (book: Book) => void) => () => {
    const book = presence.value!;
    actions.closeBookMenu();
    act(book);
  };
  const book = presence.mounted ? presence.value : null;
  return <>
    {book ? (
      <BookActionMenu
        anchor={actions.menuAnchor ?? lastAnchor.current}
        book={book}
        canEdit={canEdit}
        deleteReason={deleteReasonOf(book)}
        more={edition.items(book).map((item) => ({ ...item, onSelect: then(() => item.onSelect()) }))}
        moveReason={move.reason}
        onChangeCover={() => actions.changeCover(book)}
        onClose={actions.closeBookMenu}
        onDelete={() => actions.requestDeleteBook(book)}
        onDownload={then(onDownload)}
        onMove={move.available ? () => {
          actions.leaveMenu();
          move.open(book, actions.menuTrigger);
        } : undefined}
        onNewChapter={shelfSlot?.newChapterAt && !demo ? then((one) => {
          const at = shelfSlot.newChapterAt!(one);
          navigate(at.path, at.query);
        }) : undefined}
        onParseSummary={then(onSummary)}
        onReadFromStart={() => actions.readFromStart(book)}
        onRename={() => actions.startRename(book)}
        onUpdateFromFile={demo ? undefined : then(update.choose)}
        state={presence.status}
        updated={book.update ? { summary: updateLine(book.update, Date.now(), words), onOpen: then(onChanges) } : undefined}
        updateReason={updateReasonOf(book)}
      />
    ) : null}
    {edition.overlay}
  </>;
}
