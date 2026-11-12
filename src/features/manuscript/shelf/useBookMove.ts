import { createElement, useState, type RefObject } from 'react';
import { RotateCcw } from 'lucide-react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ManuscriptApiClient } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { useMoveBookMutation } from '../api';
import { afterOf, dropPlace, moveReasonOf } from './book-move';
import { BookMoveDialog } from './BookMoveDialog';

interface BookMoveOptions {
  api: ManuscriptApiClient;
  /** The books of the shelf in their order. */
  books: Book[];
  /** An editor of the saga: a reader of it moves nothing. */
  canEdit: boolean;
  /** The example saga of the site: its book stays. */
  demo: boolean;
  reading: boolean;
  uploading: boolean;
  toasts: Toasts;
}

/** The refusals of a move in the words of the author. */
const moveFailure = (failure: unknown) => publicApiErrorMessage(failure, 'Не удалось переставить книгу. Попробуйте ещё раз.', {
  codes: { import_expected_order_conflict: 'Пока идёт загрузка книг, переставить нельзя.' },
  forbidden: 'Переставлять книги может владелец саги, администратор или редактор.',
});

/**
 * The move of a volume on the shelf (specs/2026-11-11-ux7-reorder.md RK2): the window «Переставить…» of the «⋯», the drop
 * of a dragged card, and the line «Книга переставлена · Отменить» — one operation, updateBook with the book to stand
 * after. `available` — there is something to move (an editor, two books or more, not the example); `reason` — why the
 * volumes stand still now (RK2.2).
 */
export function useBookMove({ api, books, canEdit, demo, reading, toasts, uploading }: BookMoveOptions) {
  const [target, setTarget] = useState<{ bookId: string; returnFocus: RefObject<HTMLElement | null> | undefined } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const mutation = useMoveBookMutation(api);
  const available = canEdit && !demo && books.length > 1;
  const reason = moveReasonOf({ reading, uploading });
  const book = target ? books.find((entry) => entry.id === target.bookId) ?? null : null;

  async function move(moved: Book, afterBookId: string | null): Promise<string | null> {
    const before = afterOf(books, moved.id);
    let placed: Book;
    try {
      placed = await mutation.mutateAsync({ bookId: moved.id, afterBookId });
    } catch (failure) {
      return moveFailure(failure);
    }
    let toastId = 0;
    const undo = async () => {
      toasts.dismiss(toastId);
      try {
        const back = await mutation.mutateAsync({ bookId: moved.id, afterBookId: before });
        toasts.push({ message: 'Порядок возвращён', detail: `«${moved.title}» — снова книга ${back.place}`, tone: 'success' });
      } catch (failure) {
        toasts.push({ message: 'Не удалось вернуть порядок.', detail: moveFailure(failure), tone: 'error' });
      }
    };
    toastId = toasts.push({
      action: createElement(Button, { onClick: () => void undo(), size: 'sm', variant: 'ghost' },
        createElement(RotateCcw, { 'aria-hidden': true, size: 14 }), 'Отменить'),
      detail: `«${moved.title}» — теперь книга ${placed.place}`,
      message: 'Книга переставлена',
      tone: 'success',
    });
    return null;
  }

  async function chooseInWindow(afterBookId: string | null) {
    if (!book || mutation.isPending) return;
    setError(null);
    const failure = await move(book, afterBookId);
    if (failure) {
      setError(failure);
      return;
    }
    setTarget(null);
  }

  /** A card let go over a side of another: the same operation, its refusal — a line of the root. */
  async function drop(draggedId: string, targetId: string, side: 'before' | 'after') {
    const dragged = books.find((entry) => entry.id === draggedId);
    const afterBookId = dropPlace(books, draggedId, targetId, side);
    if (!dragged || afterBookId === undefined || mutation.isPending) return;
    const failure = await move(dragged, afterBookId);
    if (failure) toasts.push({ message: 'Книга не переставилась', detail: failure, tone: 'error' });
  }

  return {
    available,
    reason,
    open: (opened: Book, returnFocus?: RefObject<HTMLElement | null>) => {
      setError(null);
      setTarget({ bookId: opened.id, returnFocus });
    },
    drop: (draggedId: string, targetId: string, side: 'before' | 'after') => void drop(draggedId, targetId, side),
    /** Why a dragged card stays (the reading of the saga, the upload), said by a line of the root when it is taken. */
    refuse: (why: string) => toasts.push({ message: why, tone: 'neutral' }),
    element: book ? createElement(BookMoveDialog, {
      book, books, error, moving: mutation.isPending,
      onClose: () => setTarget(null),
      onMove: (afterBookId: string | null) => void chooseInWindow(afterBookId),
      returnFocusRef: target?.returnFocus,
    }) : null,
  };
}
