import { describe, expect, it } from 'vitest';
import { hiddenIn } from '../../shared/copy/dictionary';
import type { AccountQuota } from './api';
import { bookLoadsOf, bookLoadsRefusal, fewerBooksWords, loadedBooksValue, loadsOnce, noRoomByDeleting } from './book-loads';
import { refusalWords } from './refusal-words';

/** The writer of «Бесплатно»: one saga of one book, the book loaded once (specs/2026-11-18-free-tier.md FT1). */
function freeQuota(fields: { live: number; loaded: number; limit?: number; reloads?: boolean }): AccountQuota {
  const books = { used: fields.live, limit: fields.limit ?? 1 };
  return {
    day: '2026-11-18', resetsAt: '2026-11-18T21:00:00Z',
    chatTurns: { used: 0, limit: 5 }, activeTurns: { used: 0, limit: 2 }, imports: { used: 0, limit: 5 }, images: { used: 0, limit: 0 },
    imageUploads: { used: 0, limit: 5 }, chapterDrafts: { used: 0, limit: 5 }, drawingTurns: { used: 0, limit: 5 },
    draftTurns: { used: 0, limit: 10 }, draftRewrites: { used: 0, limit: 20 }, projects: { used: 1, limit: 1 },
    account: { books, chapters: { used: 14, limit: 600 }, characters: { used: 500_000, limit: 3_000_000 } },
    ownedProjects: [{ projectId: 'own', sources: { books, chapters: { used: 14, limit: 600 }, characters: { used: 500_000, limit: 3_000_000 } } }],
    bookReloads: fields.reloads ?? false,
    booksLoaded: fields.loaded,
  };
}

// FT1.5, FT3.1: one function of the entity decides, by bookReloads, booksLoaded and account.books, whether a new book and a new
// version of a book of the author's own saga may go, and the words of why not.
describe('a book loaded once', () => {
  it('closes «Добавить книги» once every book that came into the sagas reaches the limit, by the live books', () => {
    expect(bookLoadsOf(freeQuota({ live: 1, loaded: 1 }), 'own')?.newBook).toBe('На вашем тарифе одна книга, и загружается она один раз');
    // The book was deleted: it took its place for good.
    expect(bookLoadsOf(freeQuota({ live: 0, loaded: 1 }), 'own')?.newBook).toBe('На вашем тарифе книга загружается один раз — удалённая место не освобождает');
    expect(bookLoadsOf(freeQuota({ live: 0, loaded: 0 }), 'own')).toMatchObject({ newBook: null, room: 1 });
    // A limit of the writer's own above the tier: a book more fits while the journal has room.
    expect(bookLoadsOf(freeQuota({ live: 1, loaded: 2, limit: 3 }), 'own')).toMatchObject({ newBook: null, room: 1 });
  });

  it('closes «Новая версия из файла…» and warns before the files go, whatever the count', () => {
    const loads = bookLoadsOf(freeQuota({ live: 0, loaded: 0 }), 'own')!;
    expect(loads.newVersion).toBe('На вашем тарифе книга загружается один раз');
    expect(loads.warning).toBe('На вашем тарифе книга загружается один раз: заменить её файлом потом нельзя');
  });

  it('knows nothing of a tier whose book loads again, of a saga of another owner and of a quota not read', () => {
    expect(bookLoadsOf(freeQuota({ live: 1, loaded: 5, reloads: true }), 'own')).toBeNull();
    expect(bookLoadsOf(freeQuota({ live: 1, loaded: 1 }), 'another')).toBeNull();
    expect(bookLoadsOf(undefined, 'own')).toBeNull();
    expect(bookLoadsOf(freeQuota({ live: 1, loaded: 1 }), null)).toBeNull();
  });

  // FT3.2: the refusal of the server in two cases by the live books, said the same as the reasons before the press.
  it('refuses in the two cases of the live books, with no promise of room when books are fewer', () => {
    expect(refusalWords('quota.book_reloads', freeQuota({ live: 1, loaded: 1 }))).toEqual({
      ended: 'На вашем тарифе одна книга, и загружается она один раз', returns: 'Ваша книга на месте: читать, спрашивать и править главы можно.',
    });
    expect(refusalWords('quota.book_reloads', freeQuota({ live: 0, loaded: 1 }))).toEqual({
      ended: 'Книга на вашем тарифе загружается один раз',
      returns: 'Новую версию из файла и книгу вместо удалённой загрузить нельзя. Удалённую книгу можно вернуть в «Недавно удалённых» в течение 7 дней.',
    });
    const quota = freeQuota({ live: 1, loaded: 1 });
    for (const code of ['quota.account_books', 'quota.account_chapters', 'quota.account_characters']) {
      expect(refusalWords(code, quota)?.returns).not.toContain('станет меньше');
      expect(refusalWords(code, { ...quota, bookReloads: true })?.returns).toContain('Новые поместятся, когда книг станет меньше.');
    }
  });

  // FT1.5: one rule — whether a book loads once and whether the live books are at the limit — decides the reason before the
  // press, the refusal of the server, the words of fewer books, the fit of the window and the row of «Тариф» alike.
  it('decides every place by one rule', () => {
    for (const live of [1, 0]) {
      const quota = freeQuota({ live, loaded: 1 });
      const atLimit = live === 1;
      expect(bookLoadsOf(quota, 'own')?.newBook).toBe(atLimit ? bookLoadsRefusal(quota).ended : noRoomByDeleting.replace(/\.$/u, ''));
      expect(refusalWords('quota.book_reloads', quota)).toEqual(bookLoadsRefusal(quota));
    }
    const once = freeQuota({ live: 1, loaded: 3 });
    expect(loadsOnce(once)).toBe(true);
    expect(loadsOnce({ ...once, bookReloads: true })).toBe(false);
    expect(loadsOnce(undefined)).toBe(false);
    expect(refusalWords('quota.account_chapters', once)?.returns).toBe(fewerBooksWords(once));
    expect(loadedBooksValue(once)).toBe('загружено 3 из 1 · загружается один раз');
  });

  it('says no hidden word of the dictionary', () => {
    const loads = bookLoadsOf(freeQuota({ live: 1, loaded: 1 }), 'own')!;
    for (const words of [loads.newBook!, loads.newVersion, loads.warning, noRoomByDeleting, fewerBooksWords(freeQuota({ live: 1, loaded: 1 }))]) {
      expect(hiddenIn(words, 'src/entities/account-quota/book-loads.ts')).toEqual([]);
    }
  });
});
