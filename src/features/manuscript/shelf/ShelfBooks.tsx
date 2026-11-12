import { MoreHorizontal, Play } from 'lucide-react';
import { useState, type DragEvent, type MouseEvent, type ReactNode } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import { finishReadingWord, type BookState, type BookStateKind } from '../../../entities/manuscript/preparation-copy';
import { formatCount, formatWordCount } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { LinkAction } from '../../../ui/controls/LinkAction';
import { SearchField } from '../../../ui/controls/SearchField';
import { Badge, type BadgeProps } from '../../../ui/feedback/Badge';
import { EmptyFilter } from '../../../ui/feedback/EmptyState';
import { SegmentedControl } from '../../../ui/navigation/SegmentedControl';
import { BookCover } from './BookCover';
import { BookTitleField } from './BookTitleField';
import { updateLine } from './book-revert-words';
import { findBooks, keepShelfView, largeShelfFrom, readShelfView, type ShelfView } from './shelf-view';
import { shelfEntries, type VolumeView } from './upload-files';
import type { ImportQueue } from '../../../entities/manuscript/api';
import { defaultCoverColour } from './cover-colours';
import styles from './ShelfBooks.module.css';

const tones: Readonly<Record<BookStateKind, NonNullable<BadgeProps['tone']>>> = {
  updating: 'indexing', empty: 'neutral', reading: 'indexing', waiting: 'neutral', ready: 'success', unfinished: 'error',
};

/** «Книга 3 · 14 глав · 96 тыс. слов»: the volume by its place on the shelf (1…n, specs/2026-11-11-ux7-reorder.md RK1.6) — the same «Книга N» as the places of the saga. */
export function bookMeta(book: Pick<Book, 'chapterCount' | 'place' | 'wordCount'>) {
  return `Книга ${book.place} · ${formatCount(book.chapterCount, 'глава', 'главы', 'глав')} · ${formatWordCount(book.wordCount)}`;
}

export interface ShelfBooksProps {
  books: Book[];
  /** The address of the book in the reader, at the chapter the author stopped at (SF1.1). */
  hrefOf: (book: Book) => string;
  onFinish: () => void;
  finishing: boolean;
  onOpen: (book: Book) => void;
  onOpenMenu: (book: Book, trigger: HTMLElement) => void;
  /** The book whose title is a field now (SF3.1), with its saving. */
  renaming: { bookId: string; saving: boolean } | null;
  onRename: (book: Book, title: string) => Promise<string | null>;
  onRenameEnd: () => void;
  /** The word of a book from the server (SF2.1); null while the status of the saga is not here. */
  stateOf: (book: Book) => BookState | null;
  /**
   * A card taken by the mouse and let go over a side of another moves the book (specs/2026-11-11-ux7-reorder.md RK2.1);
   * null — nothing to move (a reader, one book, the example). `reason` — why the volumes stand still now (RK2.2): a taken
   * card says it by `refuse` and stays.
   */
  move?: { onDrop: (draggedId: string, targetId: string, side: DropSide) => void; reason: string | null; refuse: (why: string) => void } | null | undefined;
  /**
   * The upload of books (specs/2026-11-11-ux7-upload.md UP3.1): each volume not on the shelf yet is a card at its place
   * among the books of the upload, drawn by the page.
   */
  upload?: { queue: ImportQueue | null; volumes: readonly VolumeView[]; card: (volume: VolumeView, shape: 'card' | 'row') => ReactNode } | undefined;
  /** The book a file is carried over now: its card is marked — the drop asks about it (specs/2026-11-11-ux9-shelf-chat.md SC1.3). */
  fileOver?: string | null | undefined;
  /**
   * «Что изменилось» of the last update of a book from a file, on its card for the 7 days the server keeps it
   * (specs/2026-11-12-ux10-book-revert.md BR3.1).
   */
  onChanges?: ((book: Book) => void) | undefined;
}

export type DropSide = 'before' | 'after';

/** The kind of data a dragged card carries: a book of this shelf, not a file for the upload. */
const bookDragType = 'application/x-canonkeeper-book';
/** Why a card of a filtered shelf stays: a place among the books found is not a place on the shelf. */
export const searchMoveReason = 'Сначала сбросьте поиск: книги переставляются среди всех книг саги.';

/** The props of a card or a row that can be taken by the mouse, and where the dragged one would land. */
type DragProps = {
  draggable?: boolean;
  'data-dragging'?: true;
  'data-drop'?: DropSide;
  onDragEnd?: () => void;
  onDragOver?: (event: DragEvent<HTMLElement>) => void;
  onDragStart?: (event: DragEvent<HTMLElement>) => void;
  onDrop?: (event: DragEvent<HTMLElement>) => void;
};

/**
 * The books of the saga (specs/2026-11-11-ux7-shelf.md): each book is one link «Читать «…»», its word and its «⋯»; there
 * is no chosen book. From 13 books — «Обложки / Списком» and «Найти книгу» (SF4.2).
 */
export function ShelfBooks(props: ShelfBooksProps) {
  const { books, move = null } = props;
  const [view, setView] = useState<ShelfView>(readShelfView);
  const [words, setWords] = useState('');
  const [drag, setDrag] = useState<{ id: string; over: { id: string; side: DropSide } | null } | null>(null);
  const large = books.length >= largeShelfFrom;
  const shown = large ? findBooks(books, words) : books;
  const list = large && view === 'list';
  // The cards of the upload stand among the books, not among the books found.
  const entries = shelfEntries(shown, words.trim() ? null : props.upload?.queue ?? null, props.upload?.volumes ?? []);
  const dragReason = move ? move.reason ?? (large && words.trim() ? searchMoveReason : null) : null;
  const sideOf = (event: DragEvent<HTMLElement>): DropSide => {
    const rect = event.currentTarget.getBoundingClientRect();
    return list ? (event.clientY < rect.top + rect.height / 2 ? 'before' : 'after') : (event.clientX < rect.left + rect.width / 2 ? 'before' : 'after');
  };
  const dragOf = (book: Book): DragProps => !move ? {} : {
    draggable: true,
    ...(drag?.id === book.id ? { 'data-dragging': true } : {}),
    ...(drag?.over?.id === book.id ? { 'data-drop': drag.over.side } : {}),
    onDragStart: (event) => {
      if (dragReason) {
        event.preventDefault();
        move.refuse(dragReason);
        return;
      }
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData(bookDragType, book.id);
      setDrag({ id: book.id, over: null });
    },
    onDragOver: (event) => {
      if (!drag || drag.id === book.id) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      const side = sideOf(event);
      if (drag.over?.id !== book.id || drag.over.side !== side) setDrag({ id: drag.id, over: { id: book.id, side } });
    },
    onDrop: (event) => {
      if (!drag) return;
      event.preventDefault();
      if (drag.id !== book.id) move.onDrop(drag.id, book.id, sideOf(event));
      setDrag(null);
    },
    onDragEnd: () => setDrag(null),
  };
  return (
    <>
      {large ? (
        <div className={styles.toolbar}>
          <SegmentedControl label="Вид книг" onValueChange={(next) => { setView(next); keepShelfView(next); }}
            options={[{ label: 'Обложки', value: 'covers' }, { label: 'Списком', value: 'list' }]} value={view} />
          <div className={styles.find}>
            <SearchField label="Найти книгу" labelHidden onChange={(event) => setWords(event.target.value)} onClear={() => setWords('')}
              placeholder="Найти книгу" value={words} />
          </div>
        </div>
      ) : null}
      {entries.length === 0 ? (
        <EmptyFilter onReset={() => setWords('')} reset="Сбросить поиск" words={`Ничего не нашлось по «${words.trim()}»`} />
      ) : list ? (
        <ul aria-label="Книги саги списком" className={styles.booksList}>
          {entries.map((entry) => entry.kind === 'book'
            ? <li key={entry.book.id}><BookEntry {...props} book={entry.book} drag={dragOf(entry.book)} shape="row" /></li>
            : <li key={entry.volume.key}>{props.upload?.card(entry.volume, 'row')}</li>)}
        </ul>
      ) : (
        <div className={styles.booksGrid}>
          {entries.map((entry) => entry.kind === 'book'
            ? <BookEntry {...props} book={entry.book} drag={dragOf(entry.book)} key={entry.book.id} shape="card" />
            : <div className={styles.volumeSlot} key={entry.volume.key}>{props.upload?.card(entry.volume, 'card')}</div>)}
        </div>
      )}
    </>
  );
}

function BookEntry({ book, drag, fileOver, finishing, hrefOf, onChanges, onFinish, onOpen, onOpenMenu, onRename, onRenameEnd, renaming, shape, stateOf }:
  ShelfBooksProps & { book: Book; drag: DragProps; shape: 'card' | 'row' }) {
  const state = stateOf(book);
  const editing = renaming?.bookId === book.id;
  const open = (event: MouseEvent<HTMLAnchorElement>) => {
    // A new tab or window keeps the link of the browser.
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    onOpen(book);
  };
  const title: ReactNode = editing
    ? <BookTitleField onCancel={onRenameEnd} onSave={(next) => onRename(book, next)} saving={renaming.saving} title={book.title} />
    : (
      <a aria-label={`Читать «${book.title}»`} className={styles.bookLink} draggable={drag.draggable ? false : undefined} href={hrefOf(book)}
        onClick={open} title={book.title}>
        <span className={styles.bookTitle}>{book.title}</span>
      </a>
    );
  const menu = (
    <IconButton aria-label={`Действия книги ${book.title}`} className={shape === 'card' ? styles.bookMenuButton : styles.rowMenuButton}
      onClick={(event) => onOpenMenu(book, event.currentTarget)}>
      <MoreHorizontal aria-hidden="true" size={18} />
    </IconButton>
  );
  const word = (
    <span className={styles.bookState}>
      {state ? <Badge tone={tones[state.kind]}>{state.word}</Badge> : <span aria-hidden="true" className={styles.stateLoading} />}
      {state?.finish ? (
        <Button className={styles.stateAction} loading={finishing} onClick={onFinish} size="sm" variant="ghost">
          <Play aria-hidden="true" size={14} />{finishReadingWord}
        </Button>
      ) : null}
    </span>
  );
  // BR3.1: «Обновлена из файла сегодня в 14:20 · Что изменилось» for the 7 days of the last update.
  const updated = book.update ? (
    <p className={shape === 'card' ? styles.bookUpdate : styles.rowUpdate}>
      <span>{updateLine(book.update)}</span>
      {onChanges ? <> · <LinkAction className={styles.stateAction} onClick={() => onChanges(book)}>Что изменилось</LinkAction></> : null}
    </p>
  ) : null;
  if (shape === 'row') {
    return (
      <article aria-label={book.title} className={styles.bookRow} data-book-id={book.id} data-file-over={fileOver === book.id || undefined} {...drag}>
        <span aria-hidden="true" className={styles.rowCover} style={{ backgroundColor: book.coverColor ?? defaultCoverColour }} />
        <span className={styles.rowVolume}>Книга {book.place}</span>
        <span className={styles.rowTitle}>{title}</span>
        {menu}
        <span className={styles.rowMeta}>{formatCount(book.chapterCount, 'глава', 'главы', 'глав')} · {formatWordCount(book.wordCount)}</span>
        {word}
        {updated}
      </article>
    );
  }
  return (
    <article aria-label={book.title} className={styles.bookCard} data-book-id={book.id} data-file-over={fileOver === book.id || undefined} {...drag}>
      <BookCover book={book} decorative />
      <div className={styles.bookInfo}>
        {title}
        {menu}
        <p className={styles.bookMeta}>{bookMeta(book)}</p>
        {word}
        {updated}
      </div>
    </article>
  );
}
