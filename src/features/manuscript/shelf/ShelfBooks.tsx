import {
  BookOpen,
  Download,
  FileUp,
  Image,
  MoreHorizontal,
  Trash2,
  X,
} from 'lucide-react';
import type { Book, BookExportFormat, ProjectJob } from '../../../entities/manuscript/api';
import { formatCount, formatWordCount } from '../../../ui/copy';
import { IconButton } from '../../../ui/controls/IconButton';
import { Badge } from '../../../ui/feedback/Badge';
import { Popover } from '../../../ui/overlays/Popover';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import { BookCover } from './BookCover';
import { isActiveImportJob } from './shelf-jobs';
import styles from './ShelfBooks.module.css';

export type MenuPosition = { left: number; top: number };

interface ShelfBooksProps {
  indexingJobFor: (bookId: string) => ProjectJob | null;
  books: Book[];
  onOpenMenu: (bookId: string, trigger: HTMLElement | null) => void;
  onOpenReader: (book: Book) => void;
  onSelectBook: (bookId: string) => void;
  selectedBookId: string | null;
}

export function ShelfBooks({
  indexingJobFor,
  books,
  onOpenMenu,
  onOpenReader,
  onSelectBook,
  selectedBookId,
}: ShelfBooksProps) {
  return (
    <div className={styles.booksGrid}>
      {books.map((book) => (
        <BookCard
          indexingJob={indexingJobFor(book.id)}
          book={book}
          isActive={book.id === selectedBookId}
          key={book.id}
          onOpenMenu={(trigger) => onOpenMenu(book.id, trigger)}
          onOpenRead={() => onOpenReader(book)}
          onSelect={() => onSelectBook(book.id)}
        />
      ))}
    </div>
  );
}

export function BookCard({
  indexingJob,
  book,
  isActive,
  onOpenMenu,
  onOpenRead,
  onSelect,
}: {
  indexingJob: ProjectJob | null;
  book: Book;
  isActive: boolean;
  onOpenMenu: (trigger: HTMLElement | null) => void;
  onOpenRead: () => void;
  onSelect: () => void;
}) {
  const displayedJob = indexingJob?.status === 'succeeded' ? null : indexingJob;
  const isIndexing = Boolean(displayedJob);
  const statusTone = displayedJob?.status === 'failed'
    ? 'error'
    : displayedJob?.status === 'canceled'
      ? 'warning'
      : isIndexing
        ? 'indexing'
    : book.status === 'ready'
      ? 'success'
      : book.status === 'error'
        ? 'error'
        : 'warning';
  const statusLabel = displayedJob?.status === 'failed'
    ? 'Подготовка не удалась'
    : displayedJob?.status === 'canceled'
      ? 'Подготовка отменена'
      : isIndexing
        ? 'Подготовка'
    : book.status === 'ready'
      ? 'Готова'
      : book.status === 'error'
        ? 'Подготовка не удалась'
        : null;
  return (
    <article
      aria-current={isActive ? 'true' : undefined}
      aria-label={book.title}
      className={isActive ? styles.activeBookCard : styles.bookCard}
      onClick={onSelect}
    >
      <BookCover book={book} />
      <IconButton aria-label={`Действия книги ${book.title}`} className={styles.bookMenuButton} onClick={(event) => { event.stopPropagation(); onOpenMenu(event.currentTarget); }}>
        <MoreHorizontal aria-hidden="true" size={18} />
      </IconButton>
      <div className={styles.bookInfo}>
        <button
          aria-label={`Открыть чтение ${book.title}`}
          className={styles.bookTitleButton}
          onClick={(event) => { event.stopPropagation(); onOpenRead(); }}
          type="button"
        >
          {book.title}
        </button>
        <p>{formatCount(book.chapterCount, 'глава', 'главы', 'глав')} · {formatWordCount(book.wordCount)}</p>
        {statusLabel ? <Badge tone={statusTone}>{statusLabel}</Badge> : null}
      </div>
    </article>
  );
}

export function BookActionMenu({
  book,
  canEditManuscript,
  indexingJob,
  onChangeCover,
  onClose,
  onDelete,
  onDownload,
  onOpenRead,
  onUpdateFromFile,
  position,
  state,
  updateDisabled,
}: BookActionsProps & { state: PresenceStatus }) {
  // У восстановленного из URL меню нет кнопки-якоря, поэтому размещаем его по центру.
  const style = position ? { left: `${position.left}px`, top: `${position.top}px` } : undefined;
  const menuClassName = position ? styles.floatingMenu : `${styles.floatingMenu} ${styles.floatingMenuCentered}`;
  return (
    <Popover
      className={menuClassName}
      label="Действия книги"
      onDismiss={() => onClose?.()}
      open={state === 'open'}
      role="menu"
      style={style}
    >
      <div className={styles.floatingMenuHeader}>
        <strong>{book.title}</strong>
        <IconButton aria-label="Закрыть меню" onClick={onClose}>
          <X aria-hidden="true" size={18} />
        </IconButton>
      </div>
      <BookActionItems
        book={book}
        canEditManuscript={canEditManuscript}
        indexingJob={indexingJob}
        onChangeCover={onChangeCover}
        onDelete={onDelete}
        onDownload={onDownload}
        onOpenRead={onOpenRead}
        onUpdateFromFile={onUpdateFromFile}
        updateDisabled={updateDisabled}
      />
    </Popover>
  );
}

interface BookActionsProps {
  book: Book;
  canEditManuscript: boolean;
  indexingJob?: ProjectJob | null;
  onChangeCover: () => void;
  onClose?: () => void;
  onDelete: () => void;
  /** Takes the whole book away as a file (specs/2026-11-05-book-export.md). */
  onDownload: (format: BookExportFormat) => void;
  onOpenRead: () => void;
  /** Rewrites the book from a new file of the same volume (specs/2026-10-27-update-book-from-file.md). */
  onUpdateFromFile?: () => void;
  position?: MenuPosition | null;
  updateDisabled?: boolean;
}

const bookExportFormats: readonly { format: BookExportFormat; label: string }[] = [
  { format: 'docx', label: 'Скачать в Word' },
  { format: 'epub', label: 'Скачать в EPUB' },
  { format: 'fb2', label: 'Скачать в FB2' },
];

function BookActionItems({ book, canEditManuscript, indexingJob, onChangeCover, onDelete, onDownload, onOpenRead, onUpdateFromFile, updateDisabled }: BookActionsProps) {
  const deleteDisabled = Boolean(indexingJob && isActiveImportJob(indexingJob));
  return (
    <>
      <button className={styles.menuItem} onClick={onOpenRead} role="menuitem" type="button">
        <BookOpen aria-hidden="true" size={17} />
        Открыть чтение
      </button>
      {canEditManuscript ? (
        <button className={styles.menuItem} onClick={onChangeCover} role="menuitem" type="button">
          <Image aria-hidden="true" size={17} />
          Изменить обложку
        </button>
      ) : null}
      {canEditManuscript && book.chapterCount > 0 ? bookExportFormats.map(({ format, label }) => (
        <button className={styles.menuItem} key={format} onClick={() => onDownload(format)} role="menuitem" type="button">
          <Download aria-hidden="true" size={17} />
          {label}
        </button>
      )) : null}
      {canEditManuscript && onUpdateFromFile ? (
        <button aria-disabled={updateDisabled || undefined} className={styles.menuItem} disabled={updateDisabled} onClick={onUpdateFromFile} role="menuitem" title={updateDisabled ? 'Обновление недоступно, пока идёт загрузка.' : undefined} type="button">
          <FileUp aria-hidden="true" size={17} />
          Обновить из файла
        </button>
      ) : null}
      {canEditManuscript ? (
        <button aria-disabled={deleteDisabled || undefined} className={styles.dangerMenuItem} disabled={deleteDisabled} onClick={onDelete} role="menuitem" title={deleteDisabled ? 'Удаление недоступно во время обработки.' : undefined} type="button">
          <Trash2 aria-hidden="true" size={17} />
          Удалить книгу
        </button>
      ) : null}
    </>
  );
}
