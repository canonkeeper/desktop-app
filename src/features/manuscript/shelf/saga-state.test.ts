import { describe, expect, it } from 'vitest';
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { sagaStateAnnouncement, sagaStateOf } from './saga-state';

const titles: Record<string, string> = { 'book-1': 'Туманный берег', 'book-2': 'Белый порт', 'book-3': 'Северный суд' };
const bookTitle = (bookId: string) => titles[bookId];

function status(overrides: Partial<IndexingStatus> = {}): IndexingStatus {
  return {
    published: { exists: true, context: { state: 'processing', currentBook: { bookId: 'book-1', position: 1, total: 1 }, retryableFailedBook: null } },
    steps: [],
    preparation: {
      remainingSeconds: 480, letterPromised: true, chaptersRead: 12, chaptersTotal: 48, booksRead: 0, booksTotal: 1,
      currentBookId: 'book-1', askable: true, books: [{ bookId: 'book-1', chaptersRead: 12, chaptersTotal: 48 }],
    },
    ...overrides,
  };
}

const ready = status({
  published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } },
  preparation: undefined,
  sagaReady: { at: '2026-11-11T10:00:00Z', books: 1, chapters: 48 },
});

const stalled = status({
  published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: { bookId: 'book-3' } } },
  preparation: undefined,
});

const stalledBook: IndexingStatus = {
  published: stalled.published,
  steps: [
    { kind: 'import', status: 'succeeded', progress: 1 },
    { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 14, totalChapters: 14 } },
    { kind: 'cards', status: 'failed', progress: 0.36, jobId: 'job-cards-7', counters: { indexedChapters: 5, totalChapters: 14 } },
    { kind: 'ready', status: 'pending', progress: null },
  ],
};

// specs/2026-11-10-ux3-saga-state.md SS2.1: the words of the line by the data of the status, and nothing of SS2.2.
describe('the line of the state of the saga', () => {
  it('reads a book with the time of the server, the question and the letter', () => {
    expect(sagaStateOf({ status: status(), bookTitle, askedSinceReady: false })).toEqual({
      kind: 'reading',
      title: 'Помощник читает книгу: 12 из 48 глав · осталось ≈ 8 мин · спросить можно уже сейчас',
      note: 'Страницу можно закрыть — напишем на почту, когда дочитаю',
      ask: true,
      progress: 0.25,
    });
    const early = status({ preparation: { ...status().preparation!, askable: false, letterPromised: false, remainingSeconds: null } });
    expect(sagaStateOf({ status: early, bookTitle, askedSinceReady: false })).toMatchObject({
      title: 'Помощник читает книгу: 12 из 48 глав · обычно 10–30 минут · спросить можно будет через пару минут',
      note: 'Страницу можно закрыть — чтение продолжится',
      ask: false,
    });
  });

  // specs/2026-11-11-import-speed.md IS2.3: before the server counts the reading, a new book on its way to the shelf.
  it('tells a new book on its way to the shelf before its reading', () => {
    const none = status({ published: { exists: false, context: null }, preparation: undefined });
    expect(sagaStateOf({ status: none, bookTitle, askedSinceReady: false })).toBeNull();
    const loading = sagaStateOf({ status: none, bookTitle, askedSinceReady: false, importing: true });
    expect(loading).toEqual({ kind: 'loading', title: 'Загружаю книгу в сагу…', note: 'Сейчас она появится здесь, и помощник начнёт её читать' });
    expect(sagaStateAnnouncement(loading)).toBe('Загружаю книгу в сагу…');
    expect(sagaStateOf({ status: none, bookTitle, askedSinceReady: false, importing: true, fileName: 'Том 4.epub' })?.title)
      .toBe('Загружаю «Том 4.epub» в сагу…');
    // Once the server counts the reading, the line is the reading's.
    expect(sagaStateOf({ status: status(), bookTitle, askedSinceReady: false, importing: true })?.kind).toBe('reading');
  });

  it('names the file of a new book while the server parses it', () => {
    const parsing = status({ preparation: { ...status().preparation!, currentBookId: null, chaptersRead: 48, booksRead: 1 } });
    expect(sagaStateOf({ status: parsing, bookTitle, fileName: 'Том 2.fb2', askedSinceReady: false }))
      .toMatchObject({ kind: 'reading', title: 'Читаю файл «Том 2.fb2»… · спросить можно уже сейчас', progress: null });
  });

  it('tells the end until a question is asked after it', () => {
    expect(sagaStateOf({ status: ready, bookTitle, askedSinceReady: false }))
      .toEqual({ kind: 'ready', title: 'Книга прочитана — можно спрашивать', note: '1 книга, 48 глав' });
    expect(sagaStateOf({ status: ready, bookTitle, askedSinceReady: true })).toBeNull();
    const many = { ...ready, sagaReady: { at: ready.sagaReady!.at, books: 3, chapters: 75 } };
    expect(sagaStateOf({ status: many, bookTitle, askedSinceReady: false })).toMatchObject({ title: 'Книги прочитаны — можно спрашивать', note: '3 книги, 75 глав' });
  });

  it('names the book the reading stopped on, its chapters read and the way on', () => {
    expect(sagaStateOf({ status: stalled, bookTitle, stalledBook, askedSinceReady: false })).toEqual({
      kind: 'stalled',
      title: 'Не дочитал «Северный суд»: 5 из 14 глав. Чтение прервалось на нашей стороне.',
      note: 'Прочитанные главы останутся — дочитать можно сейчас.',
      retry: true,
      supportCode: 'job-cards-7',
    });
    const reader = status({ ...stalled, published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: null } } });
    expect(sagaStateOf({ status: reader, bookTitle, askedSinceReady: false })).toMatchObject({ kind: 'stalled', retry: false });
  });

  it('says nothing of a saga read long ago, and announces a state once, not every count', () => {
    const quiet = { ...ready, sagaReady: undefined };
    expect(sagaStateOf({ status: quiet, bookTitle, askedSinceReady: false })).toBeNull();
    const a = sagaStateAnnouncement(sagaStateOf({ status: status(), bookTitle, askedSinceReady: false }));
    const later = status({ preparation: { ...status().preparation!, chaptersRead: 30, remainingSeconds: 120 } });
    expect(sagaStateAnnouncement(sagaStateOf({ status: later, bookTitle, askedSinceReady: false }))).toBe(a);
  });

  it('never says the words of the machinery', () => {
    const words = [status(), ready, stalled].map((each) => JSON.stringify(sagaStateOf({ status: each, bookTitle, stalledBook, askedSinceReady: false }))).join(' ');
    expect(words).not.toMatch(/карточк|этап|очеред|импорт|обработк|поиск по тексту|нити и арки|Остановить/iu);
  });
});

// SS2.6 → specs/2026-11-11-ux7-shelf.md SF2.1: the word of each book is `bookStateOf` of the dictionary of the reading
// (src/entities/manuscript/preparation-copy.test.ts).

// specs/2026-11-12-desk-window.md WN3.2: the reading that waits for the helper is one line on the shelf — no count, no time,
// no letter, no question to ask and no bar — and the screen reader hears it as it is.
describe('the line of a saga whose reading waits for the helper', () => {
  it('says the helper is not connected and nothing else', () => {
    const waiting = status({
      published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: { bookId: 'book-1' } } },
      preparation: {
        remainingSeconds: null, letterPromised: false, chaptersRead: 0, chaptersTotal: 48, booksRead: 0, booksTotal: 1,
        currentBookId: 'book-1', askable: true, books: [{ bookId: 'book-1', chaptersRead: 0, chaptersTotal: 48 }], waiting: 'helper',
      },
    });
    const view = sagaStateOf({ status: waiting, bookTitle, askedSinceReady: false });
    expect(view).toEqual({ kind: 'waiting', title: 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас', note: '' });
    expect(sagaStateAnnouncement(view)).toBe('Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас');
  });
});
