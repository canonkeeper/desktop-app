import { useQueryClient } from '@tanstack/react-query';
import { type Dispatch, type SetStateAction, useEffect, useRef, useState } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ManuscriptApiClient, ProjectJob } from '../../../entities/manuscript/api';
import * as manuscriptApi from '../../../entities/manuscript/api';
import * as manuscriptFeatureApi from '../api';
import type { MenuPosition } from './ShelfBooks';
import { activeJobBookId, isActiveImportJob } from './shelf-jobs';

interface ShelfBookActionsOptions {
  api: ManuscriptApiClient;
  books: Book[];
  jobs: ProjectJob[];
  canEditManuscript: boolean;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  navigateManuscript: (overrides: Record<string, string | undefined>) => void;
  routeMenuBookId: string | null;
  selectedBook: Book | null;
  selectedBookId: string;
  setNotice: Dispatch<SetStateAction<string>>;
  setSelectedBookId: Dispatch<SetStateAction<string>>;
}

export function useShelfBookActions({
  api,
  books,
  jobs,
  canEditManuscript,
  navigate,
  navigateManuscript,
  routeMenuBookId,
  selectedBook,
  selectedBookId,
  setNotice,
  setSelectedBookId,
}: ShelfBookActionsOptions) {
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [menuBookId, setMenuBookId] = useState<string | null>(routeMenuBookId);
  const [menuPosition, setMenuPosition] = useState<MenuPosition | null>(null);
  const [coverEditorBookId, setCoverEditorBookId] = useState<string | null>(null);
  const menuReturnRef = useRef<HTMLElement | null>(null);
  const deleteBookInFlightRef = useRef(false);
  const queryClient = useQueryClient();
  const openBookMutation = manuscriptFeatureApi.useOpenBookMutation(api);
  const deleteBookMutation = manuscriptFeatureApi.useDeleteBookMutation(api);

  useEffect(() => setMenuBookId(routeMenuBookId), [routeMenuBookId]);

  function openBookMenu(bookId: string, trigger: HTMLElement | null) {
    menuReturnRef.current = trigger;
    const rect = trigger?.getBoundingClientRect();
    if (rect) {
      const menuWidth = 258;
      // Меню редактора из трёх пунктов: 181 px при мыши, 207 px при сенсорном вводе (пункты по 44 px).
      const menuHeight = 207;
      const gap = 8;
      setMenuPosition({
        left: Math.max(gap, Math.min(rect.right - menuWidth, window.innerWidth - menuWidth - gap)),
        top: Math.max(gap, Math.min(rect.bottom + gap, window.innerHeight - menuHeight - gap)),
      });
    } else {
      setMenuPosition(null);
    }
    setSelectedBookId(bookId);
    setMenuBookId(bookId);
    navigateManuscript({ bookMenu: 'open', bookId });
  }

  function closeBookMenu() {
    const returnTarget = menuReturnRef.current;
    setMenuBookId(null);
    setMenuPosition(null);
    navigateManuscript({ bookMenu: undefined, bookId: selectedBookId });
    window.setTimeout(() => returnTarget?.focus(), 0);
  }

  /** Открывает книгу в читалке на первой главе. */
  async function openBook(book = selectedBook) {
    if (!book) return;
    try {
      const { book: freshBook, chapters } = await openBookMutation.mutateAsync(book.id);
      const chapter = manuscriptApi.selectPublishedChapter(chapters);
      if (!chapter) {
        setNotice('В книге пока нет глав.');
        setMenuBookId(null);
        return;
      }
      setNotice(`Открываем ${bookLabel(freshBook)}: ${chapter.title}.`);
      setMenuBookId(null);
      navigate('/manuscript/books', {
        projectId: freshBook.projectId,
        bookId: freshBook.id,
        mode: 'read',
        chapterId: chapter.id,
      });
    } catch (error) {
      setMenuBookId(null);
      setNotice(publicApiErrorMessage(error, 'Не удалось открыть книгу.'));
    }
  }

  function changeCover(book = selectedBook) {
    if (!book) return;
    if (!canEditManuscript) {
      setNotice('Менять книги этой саги может её владелец, администратор или редактор.');
      return;
    }
    setSelectedBookId(book.id);
    setMenuBookId(null);
    setMenuPosition(null);
    setCoverEditorBookId(book.id);
    navigateManuscript({ bookMenu: undefined, bookId: book.id });
  }

  function closeCoverEditor() {
    setCoverEditorBookId(null);
    window.setTimeout(() => menuReturnRef.current?.focus(), 0);
  }

  function requestDeleteBook(bookId: string) {
    if (isBookProcessing(bookId)) {
      setNotice('Удаление недоступно во время обработки.');
      return;
    }
    setSelectedBookId(bookId);
    setMenuBookId(null);
    setDeleteConfirmOpen(true);
  }

  async function deleteBook(book = selectedBook) {
    if (!book) return;
    if (deleteBookMutation.isPending || deleteBookInFlightRef.current) return;
    if (!canEditManuscript) {
      setDeleteConfirmOpen(false);
      setMenuBookId(null);
      setNotice('Менять книги этой саги может её владелец, администратор или редактор.');
      return;
    }
    if (isBookProcessing(book.id)) {
      setDeleteConfirmOpen(false);
      setNotice('Удаление недоступно во время обработки.');
      return;
    }
    deleteBookInFlightRef.current = true;
    setNotice('Удаляем книгу...');
    try {
      await deleteBookMutation.mutateAsync({ bookId: book.id, projectId: book.projectId });
      const remaining = books.filter((item) => item.id !== book.id);
      const nextBookId = remaining[0]?.id ?? '';
      setSelectedBookId(nextBookId);
      setDeleteConfirmOpen(false);
      setMenuBookId(null);
      setNotice(`Книга «${book.title}» удалена.`);
      navigateManuscript({ bookMenu: undefined, bookId: nextBookId || undefined });
    } catch (error) {
      if (isApiStatusFailure(error) && error.status === 404) {
        const remaining = books.filter((item) => item.id !== book.id);
        setSelectedBookId(remaining[0]?.id ?? '');
        setDeleteConfirmOpen(false);
        setMenuBookId(null);
        setNotice(`Книга «${book.title}» уже удалена.`);
        navigateManuscript({ bookMenu: undefined, bookId: remaining[0]?.id });
        await queryClient.invalidateQueries({
          queryKey: manuscriptApi.manuscriptQueryKeys.shelf({ projectId: book.projectId }),
          exact: true,
        });
        return;
      }
      setNotice(publicApiErrorMessage(error, 'Не удалось удалить книгу.'));
    } finally {
      deleteBookInFlightRef.current = false;
    }
  }

  return {
    changeCover,
    closeBookMenu,
    closeCoverEditor,
    coverEditorBook: books.find((book) => book.id === coverEditorBookId) ?? null,
    deleteBook,
    deleteConfirmOpen,
    deletingBook: deleteBookMutation.isPending || deleteBookInFlightRef.current,
    dismissDeleteConfirmation: () => setDeleteConfirmOpen(false),
    menuBookId,
    menuPosition,
    openBook,
    openBookMenu,
    requestDeleteBook,
  };

  function isBookProcessing(bookId: string) {
    return jobs.some((job) => isActiveImportJob(job) && activeJobBookId(job) === bookId);
  }
}

function bookLabel(book: Book) {
  return book.displayNumber ? `Книга ${book.displayNumber}` : book.title;
}
