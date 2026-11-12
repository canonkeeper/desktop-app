import { ArrowDownToLine, ArrowUpDown, ArrowUpToLine, Check, CornerDownRight, X } from 'lucide-react';
import { useState, type RefObject } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { Dialog } from '../../../ui/overlays/Dialog';
import { movePlaces } from './book-move';
import styles from './BookMoveDialog.module.css';

/** Why «Переставить» waits in the window (RK2.1). */
export const choosePlaceReason = 'Выберите, куда поставить книгу.';

export interface BookMoveDialogProps {
  /** The books of the shelf in their order. */
  books: Book[];
  book: Book;
  /** Why the last move failed, said in the window, which stays. */
  error: string | null;
  moving: boolean;
  onClose: () => void;
  onMove: (afterBookId: string | null) => void;
  returnFocusRef?: RefObject<HTMLElement | null> | undefined;
}

/**
 * «Переставить…» of a book (specs/2026-11-11-ux7-reorder.md RK2.1; board 30·42): where the book stands now and the places
 * to put it — «В начало» and «После „…“» of every other book; the place it stands at is said, not chosen. For the
 * keyboard, a phone and the voice; a mouse may drag the card on the shelf as well.
 */
export function BookMoveDialog({ books, book, error, moving, onClose, onMove, returnFocusRef }: BookMoveDialogProps) {
  const places = movePlaces(books, book.id);
  const [choice, setChoice] = useState<string | null | undefined>(undefined);
  const last = places.at(-1)?.afterBookId;
  return (
    <Dialog canDismiss={!moving} label={`Переставить «${book.title}»`} onDismiss={onClose} returnFocusRef={returnFocusRef} width={460}>
      <div className={styles.window}>
        <header className={styles.header}>
          <h2>Переставить «{book.title}»</h2>
          <IconButton aria-label="Закрыть окно" disabled={moving} onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
        </header>
        <fieldset className={styles.places} disabled={moving}>
          <legend>Сейчас это книга {book.place}. Куда её поставить?</legend>
          {places.map((place) => {
            const chosen = choice === place.afterBookId;
            const Icon = place.afterBookId === null ? ArrowUpToLine : place.afterBookId === last ? ArrowDownToLine : CornerDownRight;
            return (
              <label className={styles.place} data-chosen={chosen || undefined} data-current={place.current || undefined} key={place.afterBookId ?? 'start'}>
                <input checked={chosen} className={styles.radio} disabled={place.current} name="book-place" onChange={() => setChoice(place.afterBookId)}
                  type="radio" value={place.afterBookId ?? ''} />
                <Icon aria-hidden="true" size={16} />
                <span className={styles.label}>{place.label}</span>
                {chosen ? <Check aria-hidden="true" className={styles.check} size={15} /> : null}
              </label>
            );
          })}
        </fieldset>
        <p className={styles.drag}>Книгу можно и перетащить мышью среди книг саги.</p>
        {error ? <Alert tone="error">{error}</Alert> : null}
        <footer className={styles.actions}>
          <Button disabled={moving} onClick={onClose} variant="outline">Отмена</Button>
          <Button disabledReason={choice === undefined ? choosePlaceReason : null} loading={moving}
            onClick={() => { if (choice !== undefined) onMove(choice); }} variant="primary">
            <ArrowUpDown aria-hidden="true" size={15} />Переставить
          </Button>
        </footer>
      </div>
    </Dialog>
  );
}
