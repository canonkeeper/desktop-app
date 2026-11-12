import { describe, expect, it } from 'vitest';
import type { IndexingStatus } from './api';
import {
  bookStateOf,
  bookStateWords,
  chatReadingLine,
  finishReadingWord,
  formatRemaining,
  mapReadingLine,
  readyWords,
  remainingWords,
  shelfReadingLine,
  waitingLine,
  type Preparation,
} from './preparation-copy';

function preparation(overrides: Partial<Preparation> = {}): Preparation {
  return {
    remainingSeconds: 480,
    letterPromised: false,
    chaptersRead: 12,
    chaptersTotal: 48,
    booksRead: 0,
    booksTotal: 1,
    currentBookId: 'book-1',
    askable: true,
    books: [{ bookId: 'book-1', chaptersRead: 12, chaptersTotal: 48 }],
    ...overrides,
  };
}

const several = preparation({
  chaptersRead: 34, chaptersTotal: 75, booksRead: 2, booksTotal: 4, currentBookId: 'book-3', remainingSeconds: null,
  books: [
    { bookId: 'book-1', chaptersRead: 22, chaptersTotal: 22 },
    { bookId: 'book-2', chaptersRead: 7, chaptersTotal: 7 },
    { bookId: 'book-3', chaptersRead: 5, chaptersTotal: 14 },
    { bookId: 'book-4', chaptersRead: 0, chaptersTotal: 32 },
  ],
});

// specs/2026-11-10-ux3-saga-state.md SS2.1, SS3.1, SS3.2, SS3.5: one word — «читает» — and the counts of the server.
describe('the words of the reading of the saga', () => {
  it.each([
    [30, 'меньше минуты'],
    [480, '≈ 8 мин'],
    [3_600, '≈ 60 мин'],
    [4_800, '≈ 1 ч 20 мин'],
    [7_000, '≈ 2 ч'],
  ])('%d seconds — «%s»', (seconds, expected) => {
    expect(formatRemaining(seconds)).toBe(expected);
  });

  it('says the time of the server, or how long a book usually takes', () => {
    expect(remainingWords(480)).toBe('осталось ≈ 8 мин');
    expect(remainingWords(null)).toBe('обычно 10–30 минут');
  });

  it('reads one book, several books and the file of a new one on the shelf', () => {
    expect(shelfReadingLine(preparation(), {})).toBe('Помощник читает книгу: 12 из 48 глав · осталось ≈ 8 мин · спросить можно уже сейчас');
    expect(shelfReadingLine(preparation({ askable: false, chaptersRead: 0, chaptersTotal: 21 }), {}))
      .toBe('Помощник читает книгу: 0 из 21 главы · осталось ≈ 8 мин · спросить можно будет через пару минут');
    expect(shelfReadingLine(several, { bookTitle: (id) => (id === 'book-3' ? 'Северный суд' : undefined) }))
      .toBe('Помощник читает книги: книга 3 из 4 · «Северный суд»: 5 из 14 глав · обычно 10–30 минут · спросить можно уже сейчас');
    expect(shelfReadingLine(preparation({ currentBookId: null, booksRead: 1 }), { fileName: 'Том 2.fb2' }))
      .toBe('Читаю файл «Том 2.fb2»… · спросить можно уже сейчас');
    expect(shelfReadingLine(preparation({ currentBookId: null, askable: false, books: [], booksTotal: 0 }), {}))
      .toBe('Читаю файл… · спросить можно будет через пару минут');
  });

  it('advises the chat to ask now, and says when it may ask', () => {
    expect(chatReadingLine(preparation())).toBe(
      'Помощник ещё читает книгу: 12 из 48 глав · осталось ≈ 8 мин. Спрашивайте уже сейчас — ответы обо всей книге станут точнее, когда он дочитает.',
    );
    expect(chatReadingLine(preparation({ askable: false }))).toBe('Помощник начал читать книгу — спросить можно будет через пару минут.');
    expect(chatReadingLine({ ...several, letterPromised: true })).toBe(
      'Помощник ещё читает книги: 34 из 75 глав · обычно 10–30 минут. Спрашивайте уже сейчас — ответы обо всей саге станут точнее, когда он дочитает. Напишем на почту, когда он дочитает.',
    );
    expect(chatReadingLine(preparation({ currentBookId: null }))).toMatch(/^Помощник читает файл новой книги/u);
  });

  it('counts the empty map and tells the end the same everywhere', () => {
    expect(mapReadingLine(preparation({ chaptersRead: 3, chaptersTotal: 12, remainingSeconds: 240 }))).toBe('Прочитано 3 из 12 глав · осталось ≈ 4 мин');
    expect(readyWords({ at: '2026-11-11T10:00:00Z', books: 1, chapters: 3 }))
      .toEqual({ title: 'Книга прочитана — можно спрашивать', facts: '1 книга, 3 главы' });
    expect(readyWords({ at: '2026-11-11T10:00:00Z', books: 3, chapters: 48 }))
      .toEqual({ title: 'Книги прочитаны — можно спрашивать', facts: '3 книги, 48 глав' });
  });

  it('never says the words the author does not know', () => {
    const lines = [
      shelfReadingLine(preparation(), {}), shelfReadingLine(several, {}), chatReadingLine(preparation()),
      chatReadingLine(preparation({ askable: false })), mapReadingLine(preparation()),
    ].join(' ');
    expect(lines).not.toMatch(/карточк|этап|очеред|импорт|обработк|поиск по тексту|нити и арки/iu);
  });
});

// specs/2026-11-11-ux7-shelf.md SF2.1, its table: one word of each book from the server, rows from top to bottom.
describe('the word of a book on the shelf', () => {
  const book = (id: string, overrides: { chapterCount?: number; status?: 'draft' | 'ready' | 'error' } = {}) => ({ id, chapterCount: 14, status: 'ready' as const, ...overrides });
  const reading: IndexingStatus = {
    published: { exists: true, context: { state: 'processing', currentBook: { bookId: 'book-3', position: 3, total: 4 }, retryableFailedBook: null } },
    steps: [],
    preparation: { ...several },
  };
  const read: IndexingStatus = { published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } }, steps: [] };
  const partial = (named: string | null): IndexingStatus => ({
    published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: named ? { bookId: named } : null } }, steps: [],
  });

  it('says what the server says of each book, in the order of the table', () => {
    // 1 — the book a new file rewrites; 2 — a book without chapters; 3 — the status not here yet.
    expect(bookStateOf(book('book-3'), reading, true)).toEqual({ kind: 'updating', word: 'Обновляется из файла', finish: false });
    expect(bookStateOf(book('book-5', { chapterCount: 0 }), reading)).toEqual({ kind: 'empty', word: 'Нет глав', finish: false });
    expect(bookStateOf(book('book-1'), undefined)).toBeNull();
    // 4–7 — while the helper reads.
    expect(bookStateOf(book('book-3'), reading)).toEqual({ kind: 'reading', word: 'Читается · 5 из 14 глав', finish: false });
    expect(bookStateOf(book('book-4'), reading)).toEqual({ kind: 'waiting', word: 'Ждёт чтения', finish: false });
    expect(bookStateOf(book('book-1'), reading)).toEqual({ kind: 'ready', word: 'Готова', finish: false });
    expect(bookStateOf(book('book-9', { status: 'draft' }), reading)).toEqual({ kind: 'waiting', word: 'Ждёт чтения', finish: false });
    expect(bookStateOf(book('book-1'), { published: { exists: false, context: null }, steps: [] })).toEqual({ kind: 'waiting', word: 'Ждёт чтения', finish: false });
    // 8–9 — the reading stopped: «Дочитать» stands at the book the server names, the same word as the line of the saga.
    expect(bookStateOf(book('book-2', { status: 'draft' }), partial('book-2'))).toEqual({ kind: 'unfinished', word: 'Прочитана не полностью', finish: true });
    expect(bookStateOf(book('book-3', { status: 'draft' }), partial('book-2'))).toEqual({ kind: 'unfinished', word: 'Прочитана не полностью', finish: false });
    expect(bookStateOf(book('book-3', { status: 'error' }), partial(null))).toEqual({ kind: 'unfinished', word: 'Прочитана не полностью', finish: false });
    expect(bookStateOf(book('book-1'), partial('book-2'))).toEqual({ kind: 'ready', word: 'Готова', finish: false });
    // 10 — read.
    expect(bookStateOf(book('book-1'), read)).toEqual({ kind: 'ready', word: 'Готова', finish: false });
    expect(finishReadingWord).toBe('Дочитать');
  });

  it('has a word for every state and no word the author does not know', () => {
    const words = Object.values(bookStateWords);
    expect(words.every((word) => word.trim().length > 0)).toBe(true);
    expect(words.join(' ')).not.toMatch(/карточк|этап|очеред|импорт|обработк|индекс|подготов/iu);
  });
});

// specs/2026-11-12-desk-window.md WN3.2: while the reading waits for the helper there is one line — on the shelf, in the
// chat, on the map and at the end of an upload — without a count, a time, a letter or a button; a book of it waits for
// the reading; without the field every line is as it was.
describe('the reading that waits for the helper', () => {
  const waiting = preparation({ chaptersRead: 0, remainingSeconds: null, waiting: 'helper' });
  const line = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';

  it('is one line wherever the reading is said', () => {
    expect(waitingLine(waiting)).toBe(line);
    expect(shelfReadingLine(waiting, {})).toBe(line);
    expect(shelfReadingLine({ ...several, waiting: 'helper' }, {})).toBe(line);
    expect(shelfReadingLine({ ...waiting, currentBookId: null }, { fileName: 'Застава у перевала.md' })).toBe(line);
    expect(chatReadingLine(waiting)).toBe(line);
    expect(chatReadingLine({ ...waiting, askable: false })).toBe(line);
    expect(mapReadingLine(waiting)).toBe(line);
    expect(line).not.toMatch(/\d|≈|почт|минут/u);
  });

  it('leaves the lines of a reading that goes on as they were', () => {
    expect(waitingLine(preparation())).toBeNull();
    expect(waitingLine(undefined)).toBeNull();
    expect(shelfReadingLine(preparation(), {})).toBe('Помощник читает книгу: 12 из 48 глав · осталось ≈ 8 мин · спросить можно уже сейчас');
  });

  it('lets a book of the saga wait, not read', () => {
    const status: IndexingStatus = {
      published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: { bookId: 'book-1' } } },
      steps: [],
      preparation: waiting,
    };
    expect(bookStateOf({ id: 'book-1', chapterCount: 48, status: 'ready' }, status)).toEqual({ kind: 'waiting', word: 'Ждёт чтения', finish: false });
  });
});
