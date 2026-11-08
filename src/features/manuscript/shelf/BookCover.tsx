import type { Book } from '../../../entities/manuscript/api';
import { useState } from 'react';
import styles from './BookCover.module.css';

export function BookCover({ book }: { book: Book }) {
  const [failedUrl, setFailedUrl] = useState<string | null>(null);
  const showImage = Boolean(book.coverUrl && failedUrl !== book.coverUrl);
  return (
    <div className={styles.cover} data-has-image={showImage} style={{ backgroundColor: book.coverColor ?? '#4F46E5' }}>
      {showImage ? <>
        <img aria-hidden="true" className={styles.coverBackdrop} decoding="async" loading="lazy" src={book.coverUrl!} />
        <img
          alt={`Обложка ${book.title}`}
          className={styles.coverImage}
          decoding="async"
          loading="lazy"
          onError={() => setFailedUrl(book.coverUrl)}
          src={book.coverUrl!}
        />
      </> : null}
      <span className={styles.caption}>{shortTitle(book.title)}</span>
    </div>
  );
}

function shortTitle(title: string) {
  return title.replace(/^Книга [IVX]+\. /, '');
}
