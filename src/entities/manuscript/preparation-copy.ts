import { formatCount, pluralize } from '../../ui/copy';
import type { Book, IndexingStatus } from './api';

/**
 * The words of the reading of the saga (specs/2026-11-10-ux3-saga-state.md): one verb — «читает» — and the same counts
 * on the shelf, in the chat, on the map and in the window of the upload. Every number is the server's (`preparation`,
 * `sagaReady` of getIndexingStatus); the page counts nothing and makes no estimate of its own.
 */
export type Preparation = NonNullable<IndexingStatus['preparation']>;
export type SagaReady = NonNullable<IndexingStatus['sagaReady']>;

/**
 * What a reading waits for, in the one line of the author (specs/2026-11-12-desk-window.md WN3.2): no count, no time, no
 * letter and no button — nobody reads the books, and the books are read and searched all the same.
 */
const waitingWords: Record<NonNullable<Preparation['waiting']>, string> = {
  helper: 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас',
};

/** The line of a reading that waits — the same on the shelf, in the chat, on the map, at the end of an upload; null while it reads. */
export function waitingLine(preparation: Preparation | null | undefined) {
  return preparation?.waiting ? waitingWords[preparation.waiting] : null;
}

/** «≈ 8 мин», «меньше минуты», «≈ 1 ч 20 мин»: the estimate of the server, minutes up to an hour, then hours and tens of minutes. */
export function formatRemaining(seconds: number) {
  if (seconds < 60) return 'меньше минуты';
  const minutes = Math.ceil(seconds / 60);
  if (minutes <= 60) return `≈ ${minutes} мин`;
  let hours = Math.floor(seconds / 3_600);
  let rest = Math.ceil((seconds - hours * 3_600) / 600) * 10;
  if (rest === 60) {
    hours += 1;
    rest = 0;
  }
  return rest === 0 ? `≈ ${hours} ч` : `≈ ${hours} ч ${rest} мин`;
}

/** «осталось ≈ 8 мин» by the estimate of the server; without it — how long a book usually takes (SS2.1). */
export function remainingWords(seconds: number | null | undefined) {
  return seconds === null || seconds === undefined ? 'обычно 10–30 минут' : `осталось ${formatRemaining(seconds)}`;
}

/** «12 из 48 глав», «1 из 21 главы». */
export function chaptersOf(read: number, total: number) {
  return `${read} из ${total} ${pluralize(total, 'главы', 'глав', 'глав')}`;
}

/** Whether a question may be asked: the search finds a chapter already, or not yet. */
export function askWords(askable: boolean) {
  return askable ? 'спросить можно уже сейчас' : 'спросить можно будет через пару минут';
}

/** The second line of the shelf: the page may be closed; the letter only when the server promises it (FS5.6). */
export function closeWords(letterPromised: boolean) {
  return letterPromised ? 'Страницу можно закрыть — напишем на почту, когда дочитаю' : 'Страницу можно закрыть — чтение продолжится';
}

/** The book the helper reads now and where it stands among the books of the saga, by the server's order. */
export function currentReading(preparation: Preparation) {
  const index = preparation.books.findIndex((book) => book.bookId === preparation.currentBookId);
  return index < 0 ? null : { ...preparation.books[index]!, position: index + 1, total: preparation.books.length };
}

/**
 * The line of the shelf while the saga is read (SS2.1): the file of a new book still parsed — without a count; one book
 * — the chapters read of the saga; several — which book of how many and its chapters; then the time and whether to ask.
 */
export function shelfReadingLine(preparation: Preparation, names: { bookTitle?: (bookId: string) => string | undefined; fileName?: string | null }) {
  const waiting = waitingLine(preparation);
  if (waiting) return waiting;
  const ask = askWords(preparation.askable);
  const current = currentReading(preparation);
  if (!current) return `Читаю файл${names.fileName ? ` «${names.fileName}»` : ''}… · ${ask}`;
  const remaining = remainingWords(preparation.remainingSeconds);
  if (current.total <= 1) {
    return `Помощник читает книгу: ${chaptersOf(preparation.chaptersRead, preparation.chaptersTotal)} · ${remaining} · ${ask}`;
  }
  const title = names.bookTitle?.(current.bookId);
  const book = `${title ? `«${title}»: ` : ''}${chaptersOf(current.chaptersRead, current.chaptersTotal)}`;
  return `Помощник читает книги: книга ${current.position} из ${current.total} · ${book} · ${remaining} · ${ask}`;
}

/**
 * The advice over the field of the chat (SS3.1): the reading by the same counts as the shelf, that a question may be asked
 * now and becomes more exact about the whole book once it is read — or, before the search finds a chapter, that it may
 * be asked in a couple of minutes.
 */
export function chatReadingLine(preparation: Preparation) {
  const waiting = waitingLine(preparation);
  if (waiting) return waiting;
  const several = preparation.booksTotal > 1;
  const what = several ? 'книги' : 'книгу';
  if (!preparation.askable) return `Помощник начал читать ${what} — спросить можно будет через пару минут.`;
  const letter = preparation.letterPromised ? ' Напишем на почту, когда он дочитает.' : '';
  if (!preparation.currentBookId) {
    return `Помощник читает файл новой книги. Спрашивайте уже сейчас — о прочитанных книгах он ответит.${letter}`;
  }
  return `Помощник ещё читает ${what}: ${chaptersOf(preparation.chaptersRead, preparation.chaptersTotal)} · `
    + `${remainingWords(preparation.remainingSeconds)}. Спрашивайте уже сейчас — ответы обо всей ${several ? 'саге' : 'книге'} `
    + `станут точнее, когда он дочитает.${letter}`;
}

/** The count of the empty map (SS3.2): «Прочитано 3 из 12 глав · осталось ≈ 4 мин». */
export function mapReadingLine(preparation: Preparation) {
  const waiting = waitingLine(preparation);
  if (waiting) return waiting;
  return `Прочитано ${chaptersOf(preparation.chaptersRead, preparation.chaptersTotal)} · ${remainingWords(preparation.remainingSeconds)}`;
}

/** The end of the reading in the same words on the shelf and in the strip under the top bar (SS2.1, SS3.5). */
export function readyWords(ready: SagaReady) {
  return {
    title: ready.books > 1 ? 'Книги прочитаны — можно спрашивать' : 'Книга прочитана — можно спрашивать',
    facts: `${formatCount(ready.books, 'книга', 'книги', 'книг')}, ${formatCount(ready.chapters, 'глава', 'главы', 'глав')}`,
  };
}

/** The word of `restartIndexing` wherever it stands — the line of the saga and the book it stopped on (SF2.4). */
export const finishReadingWord = 'Дочитать';

/** The words of a book on the shelf (specs/2026-11-11-ux7-shelf.md SF2.1); «Читается» carries the counts of the server. */
export const bookStateWords = {
  updating: 'Обновляется из файла',
  empty: 'Нет глав',
  reading: 'Читается',
  waiting: 'Ждёт чтения',
  ready: 'Готова',
  unfinished: 'Прочитана не полностью',
} as const;

export type BookStateKind = keyof typeof bookStateWords;

/** One word of a book; `finish` — «Дочитать» stands at it: the server names it for the restart of the reading. */
export interface BookState {
  kind: BookStateKind;
  word: string;
  finish: boolean;
}

const stateOf = (kind: BookStateKind, word: string = bookStateWords[kind], finish = false): BookState => ({ kind, word, finish });

/**
 * The table of SF2.1, its rows from top to bottom: the book a new file rewrites (`updating` — an active import of its
 * update); a book without chapters; while the helper reads — the current book with its counts, the books after it and
 * those read; a stopped reading — the book the server names (with «Дочитать»), the others by the state of their text;
 * else «Готова». Null only while the status of the saga is not here yet: that is loading, not a state.
 */
export function bookStateOf(book: Pick<Book, 'id' | 'chapterCount' | 'status'>, status: IndexingStatus | undefined, updating = false): BookState | null {
  if (updating) return stateOf('updating');
  if (book.chapterCount === 0) return stateOf('empty');
  if (!status) return null;
  const preparation = status.preparation;
  if (preparation) {
    const entry = preparation.books.find((candidate) => candidate.bookId === book.id);
    if (!entry || entry.chaptersRead < entry.chaptersTotal) {
      // A reading that waits reads no book: each waits for it (specs/2026-11-12-desk-window.md WN3.2).
      return entry && book.id === preparation.currentBookId && !preparation.waiting
        ? stateOf('reading', `${bookStateWords.reading} · ${chaptersOf(entry.chaptersRead, entry.chaptersTotal)}`)
        : stateOf('waiting');
    }
    return stateOf('ready');
  }
  if (!status.published.exists) return stateOf('waiting');
  const context = status.published.context;
  if (context?.state === 'partial') {
    if (context.retryableFailedBook?.bookId === book.id) return stateOf('unfinished', undefined, true);
    if (book.status !== 'ready') return stateOf('unfinished');
  }
  return stateOf('ready');
}
