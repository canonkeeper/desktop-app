import { placedBook } from './books';
import type { MockRepositoryContext } from './context';

/**
 * The number a chapter title writes — «Глава 12. Белый порт», «Глава XIV — Письмо», «12. Белый порт», «Глава 581.1: Переход» —
 * and the title without it, its spaces collapsed; none for a title without a number. The one rule of the server
 * (`sagamemory.TitledNumber`, specs/2026-11-15-reader-chapter-number.md RN1.1, RN1.5): both read the table
 * `backend/internal/domain/sagamemory/testdata/chapter-titles.json`.
 */
export function titledNumber(title: string) {
  const name = title.trim().split(/\s+/u).filter(Boolean).join(' ');
  const titled = /^глава\s+(\d+(?:\.\d+)?|[IVXLCDM]+)(?:\s*[.:—–-]\s*|\s+|$)(.*)$/u.exec(name.replace(/^Глава/iu, 'глава'));
  const numbered = titled ? null : /^(\d+(?:\.\d+)?)\s*[.)]\s+(.+)$/u.exec(name);
  const found = titled ?? numbered;
  return found ? { number: found[1]!, name: found[2]!.trim() } : { number: '', name };
}

/** The number of a living chapter and its title without it, as the reader shows them: the number its title writes or its place (`sagamemory.NumberedTitle`). */
export function numberedTitle(title: string, inBook: number) {
  const titled = titledNumber(title);
  return titled.number ? titled : { number: String(inBook), name: titled.name };
}

/**
 * The place of a living chapter of the mock in the contents of the reader — the living chapters of its book with a published
 * text (RN1.2, RN1.5). The books of the example are sparse — «Белый порт» is chapter 12 of 48, and the mock keeps none of
 * the eleven before it —, so the place is the stored order less the chapters of the book before it the reader does not
 * show: the deleted ones and the living ones without a published text. A book the mock filled itself counts exactly the
 * chapters of its contents, and a deletion leaves no gap in the reader and in the places alike.
 */
export function placeInBook(context: MockRepositoryContext, chapter: { bookId: string; order: number }) {
  const before = (item: { bookId: string; order: number }) => item.bookId === chapter.bookId && item.order < chapter.order;
  return chapter.order
    - context.state.deletedChapters.filter((source) => before(source.resource)).length
    - context.state.chapters.filter((item) => before(item) && item.status !== 'published' && item.status !== 'stale').length;
}

/**
 * The place of a chapter as the server names it beside its key — «Книга 2, гл. 12 „Белый порт“»
 * (specs/2026-11-10-ux1-names.md N1.1; `sagamemory.Chapter.Place` of the backend). The mock answers as the server
 * does; both read the table `backend/internal/domain/sagamemory/testdata/places.json`.
 */
export function chapterPlace(bookOrder: number, inBook: number, title: string) {
  const { number, name } = numberedTitle(title, inBook);
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
      places.set(chapterKey(book.order, chapter.order), chapterPlace(book.place, placeInBook(context, chapter), chapter.title));
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
        key, label: chapterPlace(book.place, placeInBook(context, chapter), chapter.title), chapterId: chapter.id, bookId: book.id,
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
