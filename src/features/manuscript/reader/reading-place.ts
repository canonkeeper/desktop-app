/**
 * The place of reading of each book in this browser (specs/2026-11-11-ux7-shelf.md SF1.1): the chapter the author read
 * last. The reader writes it when a chapter opens; the card of the shelf reads it and opens the book there. There is no
 * place of reading on the server, and the shelf keeps none of its own. The place inside the chapter is the tab's
 * (`use-reader-scroll.ts`).
 */
const storageKey = 'canonkeeper.reader.places.v1';
/** The books remembered at most: the latest read ones. */
const keptBooks = 200;

type Places = Record<string, string>;

function read(): Places {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? '{}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, string] => typeof entry[1] === 'string'));
  } catch {
    return {};
  }
}

function write(places: Places) {
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(places));
  } catch {
    // A private window may refuse the storage: the book then opens at its first chapter.
  }
}

/** The chapter of the book the author read last in this browser, or null. */
export function readingPlaceOf(bookId: string): string | null {
  return read()[bookId] ?? null;
}

/** The chapter open now becomes the place of its book; the book goes to the end of the order of the latest. */
export function rememberReadingPlace(bookId: string, chapterId: string) {
  const places = read();
  if (places[bookId] === chapterId && Object.keys(places).at(-1) === bookId) return;
  delete places[bookId];
  places[bookId] = chapterId;
  const books = Object.keys(places);
  for (const old of books.slice(0, Math.max(0, books.length - keptBooks))) delete places[old];
  write(places);
}
