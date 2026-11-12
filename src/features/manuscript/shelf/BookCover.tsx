import type { Book } from '../../../entities/manuscript/api';
import { useState } from 'react';
import styles from './BookCover.module.css';
import { defaultCoverColour } from './cover-colours';

/**
 * The cover of a book: its picture over its colour, its title on it in two lines at most. `decorative` — the card names the
 * book by its link, the cover says nothing to a screen reader.
 */
export function BookCover({ book, decorative = false }: { book: Pick<Book, 'coverColor' | 'coverUrl' | 'title'>; decorative?: boolean }) {
  const [failedUrl, setFailedUrl] = useState<string | null>(null);
  const showImage = Boolean(book.coverUrl && failedUrl !== book.coverUrl);
  return (
    <div aria-hidden={decorative || undefined} className={styles.cover} data-has-image={showImage} style={{ backgroundColor: book.coverColor ?? defaultCoverColour }}>
      {showImage ? <>
        <img aria-hidden="true" className={styles.coverBackdrop} decoding="async" loading="lazy" src={book.coverUrl!} />
        <img
          alt={decorative ? '' : `Обложка ${book.title}`}
          className={styles.coverImage}
          decoding="async"
          loading="lazy"
          onError={() => setFailedUrl(book.coverUrl)}
          src={book.coverUrl!}
        />
      </> : null}
      <span className={styles.caption}><span className={styles.captionText}>{shortTitle(book.title)}</span></span>
    </div>
  );
}

function shortTitle(title: string) {
  return title.replace(/^Книга [IVX]+\. /, '');
}
