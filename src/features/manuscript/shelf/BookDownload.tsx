import { useMutation } from '@tanstack/react-query';
import { Check, Download, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { useBookDrawingCountsQuery, type Book, type BookExportFormat, type ManuscriptApiClient } from '../../../entities/manuscript/api';
import { bytesOf, zipFiles } from '../../../shared/files/zip';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { formatCount } from '../../../ui/copy';
import { Alert } from '../../../ui/feedback/Alert';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { saveFile } from '../../../ui/save-file';
import styles from './BookMoveDialog.module.css';

/** The kinds of a file of a book, each with what it is for (specs/2026-11-11-ux9-shelf-chat.md SC1.5). */
const kinds: readonly { format: BookExportFormat; label: string; hint: string }[] = [
  { format: 'docx', label: 'Word (.docx)', hint: 'для Word и Google Документов' },
  { format: 'epub', label: 'EPUB', hint: 'для читалок и телефона' },
  { format: 'fb2', label: 'FB2', hint: 'для читалок' },
];

export interface DownloadDialogProps {
  /** «Скачать книгу «…»» or «Скачать всю сагу «…»». */
  heading: string;
  /** The books of a saga that go into its archive; absent for one book. */
  sagaBooks?: number | undefined;
  /**
   * The build has drawings: the books the file takes and where the server counts their drawings; the window offers them
   * in the file (specs/2026-11-12-ux10-book-pictures.md BP3.1). Null — a build without drawings says nothing of them.
   */
  drawings: DownloadDrawings | null;
  busy: boolean;
  /** «Готовлю книгу 2 из 5 — «…»» while a saga is made. */
  progress?: string | null | undefined;
  error?: string | null | undefined;
  onClose: () => void;
  /** The kind of the file and whether the drawings of the book go into it. */
  onDownload: (format: BookExportFormat, drawings: boolean) => void;
}

/** The books of the window and the client that counts the drawings standing in them. */
export interface DownloadDrawings {
  api: Pick<ManuscriptApiClient, 'getBookDrawingCounts'>;
  projectId: string;
  bookIds: readonly string[];
}

/**
 * «Скачать книгу…» of the «⋯» of a book and «Скачать всю сагу…» of the menu of the saga (SC1.5, L-54, Ф-13, W-5H): one
 * window — the kind of the file with what each is for, what the file holds, and, where the books have drawings, «С
 * рисунками (N)» / «Без рисунков» (specs/2026-11-12-ux10-book-pictures.md BP3.1–BP3.3): N is the server's count of the
 * drawings standing in the books, read as the window opens; with drawings is the choice first. While the count is read
 * and when it cannot be read the file is asked with the drawings: a book without any gives the same file.
 */
export function DownloadDialog({ busy, drawings, error, heading, onClose, onDownload, progress, sagaBooks }: DownloadDialogProps) {
  const [format, setFormat] = useState<BookExportFormat>('docx');
  const [withDrawings, setWithDrawings] = useState(true);
  const counts = useBookDrawingCountsQuery(drawings?.api ?? noCounts, drawings?.projectId ?? '', drawings !== null);
  const wanted = new Set(drawings?.bookIds ?? []);
  const count = counts.data ? counts.data.data.filter((book) => wanted.has(book.bookId)).reduce((sum, book) => sum + book.drawings, 0) : null;
  // The choice stands when the books have drawings, and without the number when it could not be read.
  const choice = drawings !== null && (count !== null ? count > 0 : counts.isError);
  const taken = drawings !== null && count !== 0 && withDrawings;
  return (
    <Dialog canDismiss={!busy} label={heading} onDismiss={onClose} width={480}>
      <div className={styles.window}>
        <header className={styles.header}>
          <h2>{heading}</h2>
          <IconButton aria-label="Закрыть окно" disabled={busy} onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
        </header>
        <fieldset className={styles.places} disabled={busy}>
          <legend>Какой файл скачать?</legend>
          {kinds.map((kind) => (
            <Choice chosen={format === kind.format} key={kind.format} name="book-file-kind" onChoose={() => setFormat(kind.format)} value={kind.format}>
              {kind.label} <span className={styles.hint}>— {kind.hint}</span>
            </Choice>
          ))}
        </fieldset>
        <p className={styles.drag}>
          {sagaBooks
            ? `${formatCount(sagaBooks, 'книга', 'книги', 'книг')} саги — каждая своим файлом, все в одном архиве .zip. В файле книги — главы по порядку, курсив и жирный, обложка.`
            : 'В файле — главы по порядку, курсив и жирный, обложка.'}
        </p>
        {choice ? (
          <fieldset className={styles.places} disabled={busy}>
            <legend>Рисунки</legend>
            <Choice chosen={withDrawings} name="book-file-drawings" onChoose={() => setWithDrawings(true)} value="with">
              {count ? `С рисунками (${count})` : 'С рисунками'}
            </Choice>
            <Choice chosen={!withDrawings} name="book-file-drawings" onChoose={() => setWithDrawings(false)} value="without">Без рисунков</Choice>
          </fieldset>
        ) : null}
        {choice && withDrawings ? (
          <>
            <p className={styles.drag}>Рисунки встанут там же, где в книге. Файл будет больше.</p>
            <p className={styles.fine}>Для «Новой версии из файла» лучше скачать без рисунков: файл с рисунками может быть больше 50 МБ.</p>
          </>
        ) : null}
        {drawings !== null && count === 0 ? (
          <p className={styles.drag}>
            {sagaBooks
              ? 'В книгах саги нет рисунков — их ставят в «Рисунках»: «Поставить в книгу…».'
              : 'В этой книге нет рисунков — их ставят в «Рисунках»: «Поставить в книгу…».'}
          </p>
        ) : null}
        {progress ? <p className={styles.drag} role="status">{progress}</p> : null}
        {error ? <Alert tone="error">{error}</Alert> : null}
        <footer className={styles.actions}>
          <Button disabled={busy} onClick={onClose} variant="outline">Отмена</Button>
          <Button loading={busy} onClick={() => onDownload(format, taken)} variant="primary">
            <Download aria-hidden="true" size={15} />Скачать
          </Button>
        </footer>
      </div>
    </Dialog>
  );
}

/** A window without drawings asks nothing: its query never runs. */
const noCounts: Pick<ManuscriptApiClient, 'getBookDrawingCounts'> = {
  getBookDrawingCounts: () => Promise.reject(new Error('A build without drawings counts none.')),
};

/** One line of a choice of the window: the native radio for the keyboard, the screen reader and the voice, the line its look. */
function Choice({ chosen, children, name, onChoose, value }: { chosen: boolean; children: ReactNode; name: string; onChoose: () => void; value: string }) {
  return (
    <label className={styles.place} data-chosen={chosen || undefined}>
      <input checked={chosen} className={styles.radio} name={name} onChange={onChoose} type="radio" value={value} />
      <span className={styles.label}>{children}</span>
      {chosen ? <Check aria-hidden="true" className={styles.check} size={15} /> : null}
    </label>
  );
}

/** A name of a file without the signs a system refuses in one. */
const fileName = (title: string) => title.replace(/[\\/:*?"<>|]+/gu, ' ').replace(/\s+/gu, ' ').trim() || 'Сага';

/**
 * «Скачать всю сагу…» (SC1.5): the books of the saga with text, in turn, by the same file of the server as «Скачать книгу…»,
 * and one archive of them made here — each book its own file, numbered by its place. Nothing is kept on the server.
 */
export function useSagaDownload({ api, toasts }: { api: Pick<ManuscriptApiClient, 'getBookExport'>; toasts: Toasts }) {
  const [progress, setProgress] = useState<string | null>(null);
  const mutation = useMutation({
    mutationFn: async ({ books, drawings, format, saga }: { books: readonly Book[]; drawings: boolean; format: BookExportFormat; saga: string }) => {
      const files: { name: string; bytes: Uint8Array }[] = [];
      for (const [index, book] of books.entries()) {
        setProgress(`Готовлю книгу ${index + 1} из ${books.length} — «${book.title}»`);
        // The choice of the window goes to every book of the archive (specs/2026-11-12-ux10-book-pictures.md BP3.2).
        const file = drawings ? await api.getBookExport(book.id, format, { drawings }) : await api.getBookExport(book.id, format);
        files.push({ name: `${String(book.place).padStart(2, '0')} ${file.fileName}`, bytes: await bytesOf(file.blob) });
      }
      const name = `${fileName(saga)}.zip`;
      saveFile({ blob: new Blob([zipFiles(files).slice()], { type: 'application/zip' }), fileName: name });
      return name;
    },
    onSettled: () => setProgress(null),
    onSuccess: (name) => toasts.push({ message: `Скачано: ${name}`, tone: 'success' }),
  });
  return {
    download: (saga: string, books: readonly Book[], format: BookExportFormat, drawings: boolean) => mutation.mutateAsync({ books, drawings, format, saga }),
    busy: mutation.isPending,
    progress,
    error: mutation.isError
      ? publicApiErrorMessage(mutation.error, 'Не удалось скачать сагу. Попробуйте снова.', {
        forbidden: 'Скачать сагу может её владелец, администратор или редактор.',
      })
      : null,
    reset: mutation.reset,
  };
}
