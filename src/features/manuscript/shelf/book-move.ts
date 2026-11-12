import type { Book } from '../../../entities/manuscript/api';

type ShelfBook = Pick<Book, 'id' | 'title'>;

/** A place of the window «Переставить…»: after a book, or at the start (`afterBookId` null). */
export interface MovePlace {
  afterBookId: string | null;
  label: string;
  /** The place the book stands at now: said, not chosen. */
  current: boolean;
}

/** The book a book stands after on the shelf, null for the first: where «Отменить» puts it back. */
export function afterOf(books: readonly ShelfBook[], bookId: string): string | null {
  const at = books.findIndex((book) => book.id === bookId);
  return at > 0 ? books[at - 1]!.id : null;
}

/**
 * The places a book may stand at, in the order of the shelf (specs/2026-11-11-ux7-reorder.md RK2.1): «В начало» and
 * «После „…“» of every other book, the last «— в конец»; the place it stands at now is marked «— сейчас здесь».
 */
export function movePlaces(books: readonly ShelfBook[], bookId: string): MovePlace[] {
  const now = afterOf(books, bookId);
  const others = books.filter((book) => book.id !== bookId);
  const here = ' — сейчас здесь';
  const places: MovePlace[] = [{ afterBookId: null, label: now === null ? `В начало${here}` : 'В начало', current: now === null }];
  others.forEach((other, index) => {
    const current = now === other.id;
    const end = current ? here : index === others.length - 1 ? ' — в конец' : '';
    places.push({ afterBookId: other.id, label: `После «${other.title}»${end}`, current });
  });
  return places;
}

/**
 * Where a dragged book lands when it is let go over a side of another (RK2.1): before it — after the book before it,
 * at the start without one; after it — after it. `undefined` — the book would stay where it stands.
 */
export function dropPlace(books: readonly ShelfBook[], draggedId: string, targetId: string, side: 'before' | 'after'): string | null | undefined {
  if (draggedId === targetId) return undefined;
  const rest = books.filter((book) => book.id !== draggedId);
  const at = rest.findIndex((book) => book.id === targetId);
  if (at < 0 || !books.some((book) => book.id === draggedId)) return undefined;
  const after = side === 'after' ? rest[at]!.id : at > 0 ? rest[at - 1]!.id : null;
  return after === afterOf(books, draggedId) ? undefined : after;
}

/** Why the volumes stand still now (RK2.2): the upload of books or the reading of the saga; null — they may be moved. */
export function moveReasonOf({ reading, uploading }: { reading: boolean; uploading: boolean }): string | null {
  if (uploading) return 'Пока идёт загрузка книг, переставить нельзя.';
  if (reading) return 'Пока помощник читает книги, переставить нельзя.';
  return null;
}
