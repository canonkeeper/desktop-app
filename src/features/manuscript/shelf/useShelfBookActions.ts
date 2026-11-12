import { useQueryClient } from '@tanstack/react-query';
import { createElement, useRef, useState } from 'react';
import { RotateCcw } from 'lucide-react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ManuscriptApiClient, ProjectJob } from '../../../entities/manuscript/api';
import * as manuscriptApi from '../../../entities/manuscript/api';
import { toHref } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import * as manuscriptFeatureApi from '../api';
import { readingPlaceOf } from '../reader/reading-place';
import type { MenuAnchor } from './BookActionMenu';
import { activeJobBookId, isActiveImportJob } from './shelf-jobs';

interface ShelfBookActionsOptions {
  api: ManuscriptApiClient;
  books: Book[];
  jobs: ProjectJob[];
  canEditManuscript: boolean;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  /** «Книга «…» удалена · Вернуть» in the one area of the toasts (specs/2026-11-10-ux2-patterns-actions.md PA3.3). */
  onDeleted: (book: Book) => void;
  toasts: Toasts;
}

/** The address of a book in the reader: at the chapter this browser read last (SF1.1), else from its start. */
export function readerQueryOf(book: Pick<Book, 'id' | 'projectId'>, from: 'place' | 'start' = 'place') {
  const chapterId = from === 'place' ? readingPlaceOf(book.id) ?? undefined : undefined;
  return { projectId: book.projectId, bookId: book.id, mode: 'read', chapterId };
}

export const readerHrefOf = (book: Pick<Book, 'id' | 'projectId'>) => toHref('/manuscript/books', readerQueryOf(book));

/**
 * The actions of a book on the shelf (specs/2026-11-11-ux7-shelf.md): open it, its «⋯», its title in place, its cover and
 * its deletion. Nothing of them is in the address — the shelf has no chosen book (SF1.2).
 */
export function useShelfBookActions({ api, books, canEditManuscript, jobs, navigate, onDeleted, toasts }: ShelfBookActionsOptions) {
  const [deleteBookId, setDeleteBookId] = useState<string | null>(null);
  // Why the book was not deleted, said in the window, which stays (PA2.1).
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ bookId: string; anchor: MenuAnchor | null } | null>(null);
  const [coverEditorBookId, setCoverEditorBookId] = useState<string | null>(null);
  const [renamingBookId, setRenamingBookId] = useState<string | null>(null);
  const menuReturnRef = useRef<HTMLElement | null>(null);
  const deleteBookInFlightRef = useRef(false);
  const queryClient = useQueryClient();
  const deleteBookMutation = manuscriptFeatureApi.useDeleteBookMutation(api);
  const renameMutation = manuscriptFeatureApi.useUpdateBookMutation(api);
  const bookOf = (bookId: string | null | undefined) => books.find((book) => book.id === bookId) ?? null;

  function openBookMenu(book: Book, trigger: HTMLElement) {
    menuReturnRef.current = trigger;
    const rect = trigger.getBoundingClientRect();
    setMenu({ bookId: book.id, anchor: { bottom: rect.bottom, left: rect.left, right: rect.right, top: rect.top } });
  }

  function closeBookMenu() {
    setMenu(null);
    const returnTarget = menuReturnRef.current;
    window.setTimeout(() => returnTarget?.focus(), 0);
  }

  /** The card: the book at the place of reading of this browser. */
  function openBook(book: Book) {
    navigate('/manuscript/books', readerQueryOf(book));
  }

  /** «Читать с начала»: the reader opens the first chapter of the book; one without chapters says so. */
  function readFromStart(book: Book) {
    setMenu(null);
    navigate('/manuscript/books', readerQueryOf(book, 'start'));
  }

  function startRename(book: Book) {
    setMenu(null);
    setRenamingBookId(book.id);
  }

  /** SF3.1: the new title by updateBook; «Отменить» of the line of the result is the same operation with the old one. */
  async function rename(book: Book, title: string): Promise<string | null> {
    const previous = book.title;
    try {
      await renameMutation.mutateAsync({ bookId: book.id, body: { title } });
    } catch (error) {
      return publicApiErrorMessage(error, 'Не удалось переименовать книгу. Попробуйте ещё раз.', {
        forbidden: 'Переименовать книгу может владелец саги, администратор или редактор.',
      });
    }
    setRenamingBookId(null);
    window.setTimeout(() => menuReturnRef.current?.focus(), 0);
    let toastId = 0;
    const undo = async () => {
      toasts.dismiss(toastId);
      try {
        await renameMutation.mutateAsync({ bookId: book.id, body: { title: previous } });
        toasts.push({ message: `Название возвращено: «${previous}»`, tone: 'success' });
      } catch (error) {
        toasts.push({ message: 'Не удалось вернуть название.', detail: publicApiErrorMessage(error, 'Попробуйте ещё раз.'), tone: 'error' });
      }
    };
    toastId = toasts.push({
      action: createElement(Button, { onClick: () => void undo(), size: 'sm', variant: 'ghost' },
        createElement(RotateCcw, { 'aria-hidden': true, size: 14 }), 'Отменить'),
      detail: `Было: «${previous}»`,
      message: 'Книга переименована',
      tone: 'success',
    });
    return null;
  }

  function changeCover(book: Book) {
    setMenu(null);
    setCoverEditorBookId(book.id);
  }

  function closeCoverEditor() {
    setCoverEditorBookId(null);
    window.setTimeout(() => menuReturnRef.current?.focus(), 0);
  }

  function requestDeleteBook(book: Book) {
    setMenu(null);
    setDeleteError(null);
    setDeleteBookId(book.id);
  }

  async function deleteBook(book: Book) {
    if (deleteBookMutation.isPending || deleteBookInFlightRef.current) return;
    if (!canEditManuscript || isBookUpdating(book.id)) {
      setDeleteBookId(null);
      return;
    }
    deleteBookInFlightRef.current = true;
    setDeleteError(null);
    try {
      await deleteBookMutation.mutateAsync({ bookId: book.id, projectId: book.projectId });
      setDeleteBookId(null);
      onDeleted(book);
    } catch (error) {
      if (isApiStatusFailure(error) && error.status === 404) {
        setDeleteBookId(null);
        toasts.push({ message: `Книга «${book.title}» уже удалена.`, tone: 'neutral' });
        await queryClient.invalidateQueries({ queryKey: manuscriptApi.manuscriptQueryKeys.shelf({ projectId: book.projectId }), exact: true });
        return;
      }
      setDeleteError(publicApiErrorMessage(error, 'Не удалось удалить книгу.'));
    } finally {
      deleteBookInFlightRef.current = false;
    }
  }

  /** A new file rewrites the book now: an active import of its update (SF2.1, row 1). */
  function isBookUpdating(bookId: string) {
    return jobs.some((job) => isActiveImportJob(job) && activeJobBookId(job) === bookId);
  }

  return {
    changeCover,
    closeBookMenu,
    /** The menu steps aside for a window of its item, which brings the focus back to `menuTrigger` itself. */
    leaveMenu: () => setMenu(null),
    menuTrigger: menuReturnRef,
    closeCoverEditor,
    coverEditorBook: bookOf(coverEditorBookId),
    deleteBook,
    deleteError,
    deletingBook: deleteBookMutation.isPending || deleteBookInFlightRef.current,
    deleteTarget: bookOf(deleteBookId),
    dismissDeleteConfirmation: () => setDeleteBookId(null),
    isBookUpdating,
    menuAnchor: menu?.anchor ?? null,
    menuBook: bookOf(menu?.bookId),
    openBook,
    openBookMenu,
    readFromStart,
    rename,
    renaming: renamingBookId && bookOf(renamingBookId) ? { bookId: renamingBookId, saving: renameMutation.isPending } : null,
    endRename: () => setRenamingBookId(null),
    requestDeleteBook,
    startRename,
  };
}
