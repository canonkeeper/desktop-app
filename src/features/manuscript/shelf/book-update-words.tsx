import { createContext, useContext } from 'react';
import { bookStateWords } from '../../../entities/manuscript/preparation-copy';

/**
 * The lines of the result of an update whose source is not a file of this window (specs/2026-11-15-desk-obsidian-book-web.md
 * OU3.2): a folder of the app, or a source the build does not know. A line it does not give is the neutral line of success.
 */
export interface UpdateSourceLines {
  /** «Книга «…» обновлена из Obsidian: изменена 1 глава» — the book in its words and what the update did. */
  done?: (book: string, what: string) => string;
  /** «Книга «…» не обновилась — повторите «Обновить из Obsidian…» в меню книги». */
  failed: (book: string) => string;
}

/**
 * The words of the update of a book by the edition (OU3.1): «Книги», the shell and their windows read them from one context.
 * Without its provider — the site — they are the words of the site, to the sign: the site updates a book only from a file it
 * chose. The app puts its provider over the pages and the shell (`wrapPages`): it updates a book from a file or from a folder
 * of Obsidian and does not know later which, so its words do not name the file there; nor is there a site in the app.
 */
export interface BookUpdateWords {
  /** «Обновлена из файла» before when: the line of a book, «Что изменилось» of its menu and its window (BR3.1). */
  updated: string;
  /** What the update did in the words of the question of its return (BR3.2). */
  revert: { addedOne: string; addedMany: (count: number) => string; changedAll: string };
  /** «Книга «…» обновлена из файла» of «Недавно удалённые» → «Обновления книг» (BR3.3). */
  trashTitle: (title: string) => string;
  /** While a book is updated: its mark and why «Добавить книги», the new version, its deletion and its return wait. */
  updating: { mark: string; wait: string; already: string; noDelete: string; noRevert: string };
  /** The edits of the author an update replaces (SC1.4): «Правки, сделанные на сайте…», «есть правки с сайта». */
  edits: { made: string; mark: string };
  /** The forecast of the reading under the plan of an update (R6.5): the site counts it; the app says the time of the reading by its line. */
  forecast: boolean;
  /** The books of a saga whose text a folder of the build holds: their title is no name of a file (OU3.4). */
  useFolderBooks: (projectId: string | null) => ReadonlySet<string>;
  /**
   * By the Job of an update, the lines of its source; null — the lines of a file. The build knows only the updates its windows
   * started (the server keeps no source); it does not guess the source of another one.
   */
  useSourceLines: () => (jobId: string) => UpdateSourceLines | null;
}

const noBooks: ReadonlySet<string> = new Set();

/** A build without a folder of its own: no book holds one. */
function noFolderBooks(_projectId: string | null): ReadonlySet<string> {
  return noBooks;
}

/** A build that updates a book only from a file: every line is the line of a file. */
function noSourceLines() {
  return fileLines;
}

function fileLines() {
  return null;
}

/** The words of the site (specs/2026-10-27-update-book-from-file.md, specs/2026-11-12-ux10-book-revert.md, specs/2026-11-11-ux7-trash.md). */
export const siteBookUpdateWords: BookUpdateWords = {
  updated: 'Обновлена из файла',
  revert: {
    addedOne: 'её добавил файл',
    addedMany: (count) => `Главы, которые добавил файл (${count})`,
    changedAll: 'Всё, что изменил файл',
  },
  trashTitle: (title) => `Книга «${title}» обновлена из файла`,
  updating: {
    mark: bookStateWords.updating,
    wait: 'Дождитесь, пока книга обновится из файла.',
    already: 'Книга уже обновляется из файла.',
    noDelete: 'Пока книга обновляется из файла, удалить её нельзя.',
    noRevert: 'Книга сейчас обновляется из файла — вернуть прежнюю версию можно будет после этого.',
  },
  edits: { made: 'Правки, сделанные на сайте', mark: 'есть правки с сайта' },
  forecast: true,
  useFolderBooks: noFolderBooks,
  useSourceLines: noSourceLines,
};

const BookUpdateWordsContext = createContext<BookUpdateWords>(siteBookUpdateWords);

/** The edition gives its words of the update of a book over the pages and the shell. */
export const BookUpdateWordsProvider = BookUpdateWordsContext.Provider;

export function useBookUpdateWords() {
  return useContext(BookUpdateWordsContext);
}
