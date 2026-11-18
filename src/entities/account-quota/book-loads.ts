import type { AccountQuota } from './api';

/**
 * A book of a tier where it is loaded once (specs/2026-11-18-free-tier.md FT1.5, FT3.1, FT3.2): the one rule of the page and its
 * words, read only from `getAccountQuota`. The account counts every book that ever came into its sagas (`booksLoaded`) against
 * its books (`account.books.limit`): a deleted book or saga gives no place back, and no book of the account takes a new version
 * from a file. The reasons before the press, the refusal of the server, the window of the upload and «Тариф» all ask here.
 */
export interface BookLoads {
  /** Why «Добавить книги» waits: the books of the account reached their limit; null while one more fits. */
  newBook: string | null;
  /** Why «Новая версия из файла…» waits: always, on such a tier. */
  newVersion: string;
  /** What the window of the upload says before the files go. */
  warning: string;
  /** How many books the account may still load. */
  room: number;
}

const words = {
  /** The live books at the limit: the one book is there. */
  one: 'На вашем тарифе одна книга, и загружается она один раз',
  /** Fewer live books: a deleted one took its place for good. */
  gone: 'На вашем тарифе книга загружается один раз — удалённая место не освобождает',
  version: 'На вашем тарифе книга загружается один раз',
  warning: 'На вашем тарифе книга загружается один раз: заменить её файлом потом нельзя',
} as const;

/** The tier of the writer loads a book once (FT1.1, `book_reloads` 0): his own quota says it; unknown — it loads again. */
export function loadsOnce(quota: AccountQuota | undefined): quota is AccountQuota {
  return quota?.bookReloads === false;
}

/** The live books of the account reached its books: the one book is there; else a deleted one took its place for good. */
function liveAtLimit(quota: AccountQuota) {
  const { limit, used } = quota.account.books;
  return limit > 0 && used >= limit;
}

/**
 * The books of the saga on screen, when it is the author's own and his tier loads a book once; null when a book loads
 * again, while the quota is not read or nothing limits the books, and in a saga of another owner — the key is the owner's,
 * and the refusal of the server says it there.
 */
export function bookLoadsOf(quota: AccountQuota | undefined, projectId: string | null): BookLoads | null {
  if (!loadsOnce(quota) || !projectId || !quota.ownedProjects.some((project) => project.projectId === projectId)) return null;
  const { limit } = quota.account.books;
  if (limit <= 0) return null;
  const room = Math.max(0, limit - quota.booksLoaded);
  return { newBook: room > 0 ? null : liveAtLimit(quota) ? words.one : words.gone, newVersion: words.version, warning: words.warning, room };
}

/**
 * The refusal quota.book_reloads (FT3.2) in its two cases by the live books: the one book is there, or a deleted one took its
 * place and only «Недавно удалённые» bring it back.
 */
export function bookLoadsRefusal(quota: AccountQuota | undefined): { ended: string; returns: string } {
  return quota && liveAtLimit(quota)
    ? { ended: words.one, returns: 'Ваша книга на месте: читать, спрашивать и править главы можно.' }
    : {
      ended: 'Книга на вашем тарифе загружается один раз',
      returns: 'Новую версию из файла и книгу вместо удалённой загрузить нельзя. Удалённую книгу можно вернуть в «Недавно удалённых» в течение 7 дней.',
    };
}

/** What fewer books give (FT3.1): room only where a book loads again — «Новые поместятся…» is not said on such a tier. */
export function fewerBooksWords(quota: AccountQuota | undefined) {
  return loadsOnce(quota) ? 'Ваши книги на месте.' : 'Ваши книги на месте. Новые поместятся, когда книг станет меньше.';
}

/** Where a window of the upload has more books than the journal of the account takes (FT3.1): fewer books make no room. */
export const noRoomByDeleting = `${words.gone}.`;

/** «Книги во всех сагах» of «Тариф» on such a tier (FT3.5): every book that came into the sagas against the books of the account. */
export function loadedBooksValue(quota: AccountQuota) {
  return `загружено ${quota.booksLoaded} из ${quota.account.books.limit} · загружается один раз`;
}
