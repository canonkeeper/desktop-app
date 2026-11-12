/**
 * A large shelf (specs/2026-11-11-ux7-shelf.md SF4.2): from 13 books «Обложки / Списком» — the choice this browser keeps
 * — and «Найти книгу» by title on the page. Up to 12 books neither stands.
 */
export const largeShelfFrom = 13;

export type ShelfView = 'covers' | 'list';

const storageKey = 'canonkeeper.shelf.view.v1';

export function readShelfView(): ShelfView {
  try {
    return window.localStorage.getItem(storageKey) === 'list' ? 'list' : 'covers';
  } catch {
    return 'covers';
  }
}

export function keepShelfView(view: ShelfView) {
  try {
    window.localStorage.setItem(storageKey, view);
  } catch {
    // A private window may refuse the storage: the choice then lives until the page goes.
  }
}

const fold = (text: string) => text.toLocaleLowerCase('ru-RU').replaceAll('ё', 'е').replace(/\s+/gu, ' ').trim();

/** The books whose title has the words typed, in the order of the shelf; all of them for an empty field. */
export function findBooks<T extends { title: string }>(books: readonly T[], words: string): T[] {
  const wanted = fold(words);
  return wanted ? books.filter((book) => fold(book.title).includes(wanted)) : [...books];
}
