import { placedBook } from './books';
import type { MockRepositoryContext } from './context';

/**
 * The place of a chapter as the server names it beside its key — «Книга 2, гл. 12 „Белый порт“»
 * (specs/2026-11-10-ux1-names.md N1.1; `sagamemory.Chapter.Place` of the backend). The mock answers as the server
 * does; both read the table `backend/internal/domain/sagamemory/testdata/places.json`.
 */
export function chapterPlace(bookOrder: number, order: number, title: string) {
  const clean = title.trim().split(/\s+/u).filter(Boolean).join(' ');
  const titled = /^глава\s+(\d+|[IVXLCDM]+)(?:\s*[.:—–-]\s*|\s+|$)(.*)$/u.exec(clean.replace(/^Глава/iu, 'глава'));
  const numbered = titled ? null : /^(\d+)\s*[.)]\s+(.+)$/u.exec(clean);
  const [number, name] = titled ? [titled[1]!, titled[2]!.trim()] : numbered ? [numbered[1]!, numbered[2]!.trim()] : [String(order), clean];
  return `Книга ${bookOrder}, гл. ${number}${name ? ` „${name}“` : ''}`;
}

/** The key the model names a chapter of the mock by: `b<book>c<chapter>`. */
export const chapterKey = (bookOrder: number, order: number) => `b${bookOrder}c${order}`;

/** The places of the chapters of the text of a saga by their keys, as the server writes them beside the keys (N1.3). */
export function chapterPlaces(context: MockRepositoryContext, projectId: string): Map<string, string> {
  const indexed = new Set(context.state.indexedChapterIds.published);
  const places = new Map<string, string>();
  for (const book of shelfOf(context, projectId)) {
    for (const chapter of context.state.chapters.filter((item) => item.bookId === book.id && indexed.has(item.id))) {
      places.set(chapterKey(book.order, chapter.order), chapterPlace(book.place, chapter.order, chapter.title));
    }
  }
  return places;
}

/** The chapter of the text of a saga a key names, as the saga reference gives it; none for a key the text does not have. */
export function chapterRefOf(context: MockRepositoryContext, projectId: string, key: string) {
  const indexed = new Set(context.state.indexedChapterIds.published);
  for (const book of shelfOf(context, projectId)) {
    const chapter = context.state.chapters.find((item) => item.bookId === book.id && indexed.has(item.id) && chapterKey(book.order, item.order) === key);
    if (chapter) {
      return {
        key, label: chapterPlace(book.place, chapter.order, chapter.title), chapterId: chapter.id, bookId: book.id,
        bookOrder: book.place, bookTitle: book.title, title: chapter.title,
      };
    }
  }
  return undefined;
}

/**
 * The books of a saga with their «Книга N»: the key keeps the stored order of the mock, the place counts the living books
 * of the shelf (specs/2026-11-11-ux7-reorder.md RK1.6).
 */
export function shelfOf(context: MockRepositoryContext, projectId: string) {
  const books = context.state.books.filter((item) => item.projectId === projectId);
  return books.map((book) => placedBook(books, book));
}

/** The places of the keys given: a key the text has no chapter for is left out. */
export function placesOf(context: MockRepositoryContext, projectId: string, keys: Iterable<string | null | undefined>): Record<string, string> | undefined {
  const all = chapterPlaces(context, projectId);
  const found: Record<string, string> = {};
  for (const key of keys) {
    const place = key ? all.get(key.trim()) : undefined;
    if (key && place) found[key.trim()] = place;
  }
  return Object.keys(found).length ? found : undefined;
}

/** The chapter keys of the source links in a text: `[…](source:b2c12/3)`. */
export function linkedKeys(text: string | undefined) {
  return [...(text ?? '').matchAll(/\]\(source:([^)\s/]+)/gu)].map((match) => match[1]!);
}
