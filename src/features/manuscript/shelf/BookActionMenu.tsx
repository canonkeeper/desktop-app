import { useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowUpDown, BookOpen, Download, FileUp, History, Image, ListTree, Pencil, X } from 'lucide-react';
import type { Book } from '../../../entities/manuscript/api';
import { IconButton } from '../../../ui/controls/IconButton';
import { DeleteMenuItem } from '../../../ui/overlays/DeleteMenuItem';
import { Popover } from '../../../ui/overlays/Popover';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import styles from './ShelfBooks.module.css';

/** Where the «⋯» of a book stands in the window. */
export type MenuAnchor = Pick<DOMRect, 'bottom' | 'left' | 'right' | 'top'>;
/** Where the menu of a book stands, and how tall it may be there. */
export type MenuPlace = { left: number; top: number; maxHeight: number };

const menuWidth = 258;
const gap = 8;

/**
 * SF1.4 (specs/2026-11-11-ux7-shelf.md): the menu stands at its own «⋯» and whole in the window, by the height of the menu
 * itself — under the button when it fits, else over it, else on the roomier side with a scroll inside.
 */
export function placeMenu(anchor: MenuAnchor, height: number, window: { width: number; height: number }): MenuPlace {
  const left = Math.max(gap, Math.min(anchor.right - menuWidth, window.width - menuWidth - gap));
  const below = window.height - anchor.bottom - 2 * gap;
  const above = anchor.top - 2 * gap;
  if (height <= below) return { left, top: anchor.bottom + gap, maxHeight: below };
  if (height <= above) return { left, top: anchor.top - gap - height, maxHeight: above };
  return below >= above
    ? { left, top: anchor.bottom + gap, maxHeight: below }
    : { left, top: gap, maxHeight: above };
}

export interface BookActionMenuProps {
  anchor: MenuAnchor | null;
  book: Book;
  /** An editor of the saga; a reader of it reads only — the file of a book is given to an editor (ExportBook). */
  canEdit: boolean;
  /** Why «Удалить книгу…» waits now, or null. */
  deleteReason: string | null;
  onChangeCover: () => void;
  onClose: () => void;
  onDelete: () => void;
  /** «Скачать книгу…»: the window of the kind of its file (specs/2026-11-05-book-export.md; specs/2026-11-11-ux9-shelf-chat.md SC1.5). */
  onDownload: () => void;
  /** «Переставить…» (specs/2026-11-11-ux7-reorder.md RK2.1): none for a saga of one book or the example. */
  onMove?: (() => void) | undefined;
  /** Why «Переставить…» waits now — the reading of the saga, the upload (RK2.2) — or null. */
  moveReason?: string | null;
  onReadFromStart: () => void;
  /** «Как разобралась книга» (specs/2026-11-11-ux7-upload.md UP4.2): its chapters as the file gave them. */
  onParseSummary?: (() => void) | undefined;
  onRename: () => void;
  /** Rewrites the book from a new file of the same volume (specs/2026-10-27-update-book-from-file.md); none in the example. */
  onUpdateFromFile?: (() => void) | undefined;
  state: PresenceStatus;
  /** Why «Новая версия из файла…» waits now, or null. */
  updateReason: string | null;
  /**
   * The last update of the book from a file for the 7 days its previous version can come back (specs/2026-11-11-ux7-trash.md
   * TB4.1, specs/2026-11-12-ux10-book-revert.md BR3.1): «Что изменилось» with when it was.
   */
  updated?: { summary: string; onOpen: () => void } | undefined;
}

/**
 * «⋯» of a book (SF1.4): «Читать с начала», «Новая версия из файла…» second — the way of a new version of the book
 * (specs/2026-11-11-ux9-shelf-chat.md SC1.3) — «Как разобралась книга» (specs/2026-11-11-ux7-upload.md UP4.2), «Переименовать»,
 * «Переставить…» (specs/2026-11-11-ux7-reorder.md RK2.1), «Обложка…», «Скачать книгу…» with the window of its file (SC1.5)
 * and «Удалить книгу…» last. An item that waits stays in its place,
 * dimmed, and says why under its word. The address does not know the menu; on a phone it is a sheet from the bottom (the
 * styles).
 */
export function BookActionMenu({
  anchor, book, canEdit, deleteReason, moveReason = null, onChangeCover, onClose, onDelete, onDownload, onMove, onParseSummary, onReadFromStart,
  onRename, onUpdateFromFile, state, updateReason, updated,
}: BookActionMenuProps) {
  const contentRef = useRef<HTMLDivElement>(null);
  const deleteReasonId = useId();
  const [place, setPlace] = useState<MenuPlace | null>(null);
  useLayoutEffect(() => {
    if (!anchor) return;
    // The padding and the border of the menu around its content.
    const height = (contentRef.current?.offsetHeight ?? 0) + 2 * gap + 2;
    setPlace(placeMenu(anchor, height, { width: window.innerWidth, height: window.innerHeight }));
  }, [anchor]);
  const style = anchor
    ? place
      ? { left: `${place.left}px`, top: `${place.top}px`, maxHeight: `${place.maxHeight}px` }
      : { left: `${anchor.left}px`, top: `${anchor.bottom + gap}px`, visibility: 'hidden' as const }
    : undefined;
  return (
    <Popover
      className={anchor ? styles.floatingMenu : `${styles.floatingMenu} ${styles.floatingMenuCentered}`}
      label="Действия книги"
      onDismiss={onClose}
      open={state === 'open'}
      role="menu"
      style={style}
    >
      <div ref={contentRef} role="none">
        {/* The name of the book and × for a pointer; a menu holds only its items, Esc closes it (aria-required-children). */}
        <div aria-hidden="true" className={styles.floatingMenuHeader}>
          <strong title={book.title}>{book.title}</strong>
          <IconButton aria-label="Закрыть меню" onClick={onClose} tabIndex={-1}>
            <X aria-hidden="true" size={18} />
          </IconButton>
        </div>
        {updated ? (
          <button className={styles.menuItem} onClick={updated.onOpen} role="menuitem" type="button">
            <History aria-hidden="true" size={17} />
            <span className={styles.menuItemWords}>
              Что изменилось
              <span className={styles.menuNote}>{updated.summary}</span>
            </span>
          </button>
        ) : null}
        <MenuItem icon={<BookOpen aria-hidden="true" size={17} />} label="Читать с начала" onClick={onReadFromStart} />
        {canEdit ? <>
          {onUpdateFromFile ? (
            <MenuItem icon={<FileUp aria-hidden="true" size={17} />} label="Новая версия из файла…" onClick={onUpdateFromFile} reason={updateReason} />
          ) : null}
          {onParseSummary && book.chapterCount > 0 ? (
            <MenuItem icon={<ListTree aria-hidden="true" size={17} />} label="Как разобралась книга" onClick={onParseSummary} />
          ) : null}
          <MenuItem icon={<Pencil aria-hidden="true" size={17} />} label="Переименовать" onClick={onRename} />
          {onMove ? (
            <MenuItem icon={<ArrowUpDown aria-hidden="true" size={17} />} label="Переставить…" onClick={onMove} reason={moveReason} />
          ) : null}
          <MenuItem icon={<Image aria-hidden="true" size={17} />} label="Обложка…" onClick={onChangeCover} />
          {book.chapterCount > 0 ? (
            <MenuItem icon={<Download aria-hidden="true" size={17} />} label="Скачать книгу…" onClick={onDownload} />
          ) : null}
          <DeleteMenuItem aria-describedby={deleteReason ? deleteReasonId : undefined} aria-disabled={deleteReason ? true : undefined}
            label="Удалить книгу…" onClick={deleteReason ? undefined : onDelete} />
          {deleteReason ? <span className={styles.menuReason} id={deleteReasonId}>{deleteReason}</span> : null}
        </> : null}
      </div>
    </Popover>
  );
}

function MenuItem({ icon, label, onClick, reason = null }: { icon: ReactNode; label: string; onClick: () => void; reason?: string | null }) {
  const id = useId();
  const reasonId = reason ? id : undefined;
  return (
    <button aria-describedby={reasonId} aria-disabled={reason ? true : undefined} className={styles.menuItem} onClick={reason ? undefined : onClick}
      role="menuitem" type="button">
      {icon}
      <span className={styles.menuItemWords}>
        {label}
        {reason ? <span className={styles.menuReason} id={reasonId}>{reason}</span> : null}
      </span>
    </button>
  );
}
