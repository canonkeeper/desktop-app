import type { RecentlyDeleted } from '../../entities/manuscript/api';
import { revertUntilWords, updatedWhen } from '../../features/manuscript/shelf/book-revert-words';
import { formatCount, pluralize } from '../../ui/copy';

/**
 * The words of «Недавно удалённые» (specs/2026-11-11-ux7-trash.md TB1.1, TB1.3): what a row is and where it was, when it
 * was deleted, when it goes for good, and where «Открыть» takes the author once it is back. «Вернуть», «книга», «глава»,
 * «рисунок» — never «восстановить» or «источник».
 */
export type TrashKind = 'saga' | 'book' | 'chapter' | 'drawing';
export type TrashRow =
  | { kind: 'saga'; item: RecentlyDeleted['sagas']['items'][number] }
  | { kind: 'book'; item: RecentlyDeleted['books']['items'][number] }
  | { kind: 'chapter'; item: RecentlyDeleted['chapters']['items'][number] }
  | { kind: 'drawing'; item: NonNullable<RecentlyDeleted['drawings']>['items'][number] };

/** The groups of the window in their order, with their headings. */
export const trashGroups: readonly { kind: TrashKind; heading: string }[] = [
  { kind: 'saga', heading: 'Саги' },
  { kind: 'book', heading: 'Книги' },
  { kind: 'chapter', heading: 'Главы' },
  { kind: 'drawing', heading: 'Рисунки' },
];

const day = 86_400_000;
const time = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const date = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });

/** «Глава 12 «Белый порт»», «Рисунок без названия». */
export function trashTitle(row: TrashRow) {
  switch (row.kind) {
    case 'saga':
      return `Сага «${row.item.title}»`;
    case 'book':
      // A deleted book has no number of the shelf: «Книга N» is a place among the living books (specs/2026-11-11-ux7-reorder.md RK1.6).
      return `Книга «${row.item.title}»`;
    case 'chapter':
      return `Глава ${row.item.order} «${row.item.title}»`;
    case 'drawing':
      return row.item.title.trim() ? `Рисунок «${row.item.title}»` : 'Рисунок без названия';
  }
}

/** «сегодня в 14:20», «вчера в 09:05», «3 ноября». */
export function deletedWhen(value: string, now = Date.now()) {
  const at = new Date(value);
  if (Number.isNaN(at.valueOf())) return '';
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  if (at.getTime() >= today.getTime()) return `сегодня в ${time.format(at)}`;
  if (at.getTime() >= today.getTime() - day) return `вчера в ${time.format(at)}`;
  return date.format(at);
}

/** «исчезнет через 7 дней» right after the deletion; the last day — «исчезнет в течение суток». */
export function goesIn(restoreUntil: string, now = Date.now()) {
  const left = Date.parse(restoreUntil) - now;
  if (Number.isNaN(left) || left <= 0) return 'срок вышел';
  if (left < day) return 'исчезнет в течение суток';
  return `исчезнет через ${formatCount(Math.ceil(left / day), 'день', 'дня', 'дней')}`;
}

/** The quiet line of a row: where it was, when it was deleted, when it goes for good. */
export function trashMeta(row: TrashRow, now = Date.now()) {
  const when = deletedWhen(row.item.deletedAt, now);
  const term = goesIn(row.item.restoreUntil, now);
  switch (row.kind) {
    case 'saga': {
      const books = row.item.books > 0
        ? `${formatCount(row.item.books, 'книга', 'книги', 'книг')} ${pluralize(row.item.books, 'вернётся', 'вернутся', 'вернутся')} с ней`
        : '';
      return [books, `удалена ${when}`, term].filter(Boolean).join(' · ');
    }
    case 'book':
      return [`Сага «${row.item.sagaTitle}»`, `удалена ${when}`, term].join(' · ');
    case 'chapter':
      return [`Книга ${row.item.bookDisplayNumber} «${row.item.bookTitle}»`, `сага «${row.item.sagaTitle}»`, `удалена ${when}`, term].join(' · ');
    case 'drawing':
      return [`Сага «${row.item.sagaTitle}»`, `удалён ${when}`, term].join(' · ');
  }
}

/** The line after «Вернуть»: «Книга «Туманный берег» возвращена», and where it is. */
export function returnedWords(row: TrashRow): { message: string; detail?: string } {
  switch (row.kind) {
    case 'saga':
      return { message: `Сага «${row.item.title}» возвращена` };
    case 'book':
      return { message: `Книга «${row.item.title}» возвращена`, detail: `Сага «${row.item.sagaTitle}»` };
    case 'chapter':
      return { message: `Глава «${row.item.title}» возвращена`, detail: `Книга ${row.item.bookDisplayNumber} «${row.item.bookTitle}»` };
    case 'drawing':
      return { message: `${trashTitle(row)} возвращён`, detail: `Сага «${row.item.sagaTitle}»` };
  }
}

/** A return refused by the tier: what did not come back, and that it waits in the list (TB1.3). */
export function notReturnedWords(row: TrashRow) {
  return row.kind === 'drawing'
    ? { title: `${trashTitle(row)} не вернулся.`, waits: 'Он подождёт здесь, пока не выйдет срок.' }
    : { title: `${trashTitle(row)} не вернулась.`, waits: 'Она подождёт здесь, пока не выйдет срок.' };
}

/** Where «Открыть» of a returned row takes the author: the shelf of the saga, its book, the chapter in the reader, the drawing. */
export function returnedPlace(row: TrashRow): { path: string; query: Record<string, string> } {
  switch (row.kind) {
    case 'saga':
      return { path: '/manuscript/books', query: { projectId: row.item.id } };
    case 'book':
      return { path: '/manuscript/books', query: { projectId: row.item.projectId, bookId: row.item.id } };
    case 'chapter':
      return {
        path: '/manuscript/books',
        query: { projectId: row.item.projectId, bookId: row.item.bookId, chapterId: row.item.id, mode: 'read', sourceView: 'published' },
      };
    case 'drawing':
      return { path: '/drawing', query: { projectId: row.item.projectId, drawing: row.item.id } };
  }
}

/** A book updated from a file whose version before the update can still be returned (specs/2026-11-12-ux10-book-revert.md BR3.3). */
export type BookUpdateRow = RecentlyDeleted['bookUpdates']['items'][number];

/** «Книга «Туманный берег» обновлена из файла». */
export function bookUpdateTitle(item: BookUpdateRow) {
  return `Книга «${item.title}» обновлена из файла`;
}

/** «Сага «…» · сегодня в 14:20 · можно вернуть прежнюю версию до 19 ноября». */
export function bookUpdateMeta(item: BookUpdateRow, now = Date.now()) {
  return [`Сага «${item.sagaTitle}»`, updatedWhen(item.updatedAt, now), revertUntilWords(item)].filter(Boolean).join(' · ');
}

/** The rows of a group of the answer, in its order. */
export function rowsOf(list: RecentlyDeleted, kind: TrashKind): { rows: TrashRow[]; total: number } {
  switch (kind) {
    case 'saga':
      return { rows: list.sagas.items.map((item) => ({ kind, item })), total: list.sagas.total };
    case 'book':
      return { rows: list.books.items.map((item) => ({ kind, item })), total: list.books.total };
    case 'chapter':
      return { rows: list.chapters.items.map((item) => ({ kind, item })), total: list.chapters.total };
    case 'drawing':
      return { rows: (list.drawings?.items ?? []).map((item) => ({ kind, item })), total: list.drawings?.total ?? 0 };
  }
}
