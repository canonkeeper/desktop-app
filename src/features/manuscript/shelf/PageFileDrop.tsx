import { CloudUpload, FileUp } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { bookFileKinds } from '../../../entities/manuscript/book-file-kinds';
import { Portal } from '../../../ui/overlays/Portal';
import styles from './PageFileDrop.module.css';

const carriesFiles = (event: DragEvent) => Array.from(event.dataTransfer?.types ?? []).includes('Files');

/**
 * The whole page takes a file (specs/2026-11-11-ux7-upload.md UP1.1; board 35·19): a file carried over any place of the
 * shelf — a book, the heading, the line of the saga — is taken there, and the browser never opens it in place of the page.
 * A card of the shelf carried to its new place is not a file and is let be. While the upload is closed the page says why
 * and takes nothing.
 */
export function usePageFileDrop({ blockedReason, enabled, onFiles, onRefused }: {
  blockedReason: string | null;
  enabled: boolean;
  /** The files, and the book whose card they were dropped on (specs/2026-11-11-ux9-shelf-chat.md SC1.3). */
  onFiles: (files: File[], bookId: string | null) => void;
  onRefused: (reason: string) => void;
}) {
  const [over, setOver] = useState(false);
  // The book whose card the file is carried over: the page asks about that book (SC1.3).
  const [overBook, setOverBook] = useState<string | null>(null);
  const depth = useRef(0);
  const latest = useRef({ blockedReason, onFiles, onRefused });
  latest.current = { blockedReason, onFiles, onRefused };
  useEffect(() => {
    if (!enabled) return undefined;
    const enter = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth.current += 1;
      setOver(true);
    };
    const leave = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) {
        setOver(false);
        setOverBook(null);
      }
    };
    const overPage = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = latest.current.blockedReason ? 'none' : 'copy';
      setOverBook(bookUnder(event.target));
    };
    const drop = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth.current = 0;
      setOver(false);
      setOverBook(null);
      const files = [...(event.dataTransfer?.files ?? [])];
      if (files.length === 0) return;
      if (latest.current.blockedReason) latest.current.onRefused(latest.current.blockedReason);
      else latest.current.onFiles(files, bookUnder(event.target));
    };
    window.addEventListener('dragenter', enter);
    window.addEventListener('dragleave', leave);
    window.addEventListener('dragover', overPage);
    window.addEventListener('drop', drop);
    return () => {
      window.removeEventListener('dragenter', enter);
      window.removeEventListener('dragleave', leave);
      window.removeEventListener('dragover', overPage);
      window.removeEventListener('drop', drop);
      depth.current = 0;
      setOver(false);
      setOverBook(null);
    };
  }, [enabled]);
  return { over, overBook };
}

/** The book of the card under the pointer: the card of a book on the shelf carries its id (`data-book-id`). */
function bookUnder(target: EventTarget | null) {
  const element = target instanceof Element ? target : null;
  return element?.closest('[data-book-id]')?.getAttribute('data-book-id') ?? null;
}

/**
 * «Отпустите, чтобы добавить книги» over the whole page while a file is carried over it, or why it is not taken now. The
 * books stay seen under it, and it says that a cover takes a new version of its book; over a cover it steps up to a line at
 * the top and names that book, its card marked (specs/2026-11-11-ux9-shelf-chat.md SC1.3).
 */
export function PageFileDropOverlay({ blockedReason, book, over }: { blockedReason: string | null; book?: string | null | undefined; over: boolean }) {
  if (!over) return null;
  const onBook = Boolean(book) && !blockedReason;
  return (
    <Portal>
      <div aria-hidden="true" className={styles.overlay} data-blocked={blockedReason ? '' : undefined} data-book={onBook || undefined}>
        <div className={styles.target}>
          {onBook ? <FileUp size={24} /> : <CloudUpload size={36} />}
          <strong>
            {blockedReason ? 'Сейчас книги не добавить' : onBook ? `Отпустите на «${book}» — спрошу, заменить ли её этим файлом` : 'Отпустите, чтобы добавить книги'}
          </strong>
          {onBook ? null : <span>{blockedReason ?? `${bookFileKinds} · до 50 МБ. Новая версия книги — отпустите файл на её обложке`}</span>}
        </div>
      </div>
    </Portal>
  );
}
