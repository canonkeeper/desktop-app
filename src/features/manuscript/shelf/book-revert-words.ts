import type { Book, BookRevertPreview } from '../../../entities/manuscript/api';
import { formatCount, pluralize } from '../../../ui/copy';

/**
 * The words of the last update of a book from a file and of its return (specs/2026-11-12-ux10-book-revert.md BR3): when the
 * book was updated, what the update did, until when the version before it can be returned, and the question of the return
 * in the words of its preview. The server owns the update (`Book.update`); the page only says it.
 */
export type BookUpdate = NonNullable<Book['update']>;

const day = 86_400_000;
const time = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const date = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });

/** «сегодня в 14:20», «вчера в 09:05», «3 ноября». */
export function updatedWhen(value: string, now = Date.now()) {
  const at = new Date(value);
  if (Number.isNaN(at.valueOf())) return '';
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  if (at.getTime() >= today.getTime()) return `сегодня в ${time.format(at)}`;
  if (at.getTime() >= today.getTime() - day) return `вчера в ${time.format(at)}`;
  return date.format(at);
}

/** «Обновлена из файла сегодня в 14:20». */
export function updateLine(update: Pick<BookUpdate, 'at'>, now = Date.now()) {
  return `Обновлена из файла ${updatedWhen(update.at, now)}`;
}

/** «можно вернуть прежнюю версию до 19 ноября». */
export function revertUntilWords(update: Pick<BookUpdate, 'revertUntil'>) {
  const until = new Date(update.revertUntil);
  return Number.isNaN(until.valueOf()) ? '' : `можно вернуть прежнюю версию до ${date.format(until)}`;
}

/** «Прежнюю версию можно вернуть до 19 ноября.» — the term under «Что изменилось». */
export function revertTermLine(update: Pick<BookUpdate, 'revertUntil'>) {
  const until = new Date(update.revertUntil);
  return Number.isNaN(until.valueOf()) ? '' : `Прежнюю версию можно вернуть до ${date.format(until)}.`;
}

/** «изменены 2 главы, добавлена 1 глава, удалена 1 глава»; nothing — «без изменений». */
export function bookUpdateSummary(update: Pick<BookUpdate, 'added' | 'changed' | 'removed'>) {
  const part = (count: number, one: string, few: string, many: string) => count > 0
    ? `${pluralize(count, one, few, many)} ${formatCount(count, 'глава', 'главы', 'глав')}` : '';
  const parts = [
    part(update.changed, 'изменена', 'изменены', 'изменено'),
    part(update.added, 'добавлена', 'добавлены', 'добавлено'),
    part(update.removed, 'удалена', 'удалены', 'удалено'),
  ].filter(Boolean);
  return parts.length ? parts.join(', ') : 'без изменений';
}

type Named = BookRevertPreview['kept'][number];

/** Up to three chapters named, the rest counted: «„Утро“, „Река“ и ещё 2». */
function names(chapters: readonly Named[]) {
  const named = chapters.slice(0, 3).map((chapter) => `«${chapter.title}»`).join(', ');
  return chapters.length > 3 ? `${named} и ещё ${chapters.length - 3}` : named;
}

const stays: Record<NonNullable<Named['stay']>, [one: string, many: string]> = {
  edited: ['вы правили её после обновления', 'вы правили их после обновления'],
  deleted: ['удалена после обновления', 'удалены после обновления'],
  returned: ['уже возвращена', 'уже возвращены'],
};

/** The action of the question of the return. */
export const revertAction = 'Вернуть версию до обновления';

/**
 * The question of «Вернуть прежнюю версию…» (BR3.2) by the preview of the server: what comes back, what the file added
 * and goes to «Недавно удалённые», what stays as it is and why, and that the helper reads the returned chapters again.
 */
export function revertQuestion(preview: BookRevertPreview): { title: string; line: string; also: string } {
  const back = [
    preview.text.length ? `текст ${preview.text.length} ${pluralize(preview.text.length, 'главы', 'глав', 'глав')}` : '',
    preview.place.length ? `названия и места ${preview.place.length} ${pluralize(preview.place.length, 'главы', 'глав', 'глав')}` : '',
    preview.returned.length ? formatCount(preview.returned.length, 'убранная глава', 'убранные главы', 'убранных глав') : '',
  ].filter(Boolean);
  const gone = preview.removed.length === 1
    ? `В «Недавно удалённые» уйдёт глава ${names(preview.removed)} — её добавил файл.`
    : preview.removed.length > 1 ? `Главы, которые добавил файл (${preview.removed.length}), уйдут в «Недавно удалённые».` : '';
  const line = [back.length ? `Вернутся: ${back.join(', ')}.` : '', gone].filter(Boolean).join(' ')
    || 'Всё, что изменил файл, уже поменяли после обновления — книга останется как сейчас.';
  const kept = (['edited', 'deleted', 'returned'] as const).flatMap((stay) => {
    const chapters = preview.kept.filter((chapter) => chapter.stay === stay);
    return chapters.length ? [`${names(chapters)} — ${stays[stay][chapters.length === 1 ? 0 : 1]}`] : [];
  });
  const also = [
    kept.length ? `Останутся как сейчас: ${kept.join('; ')}.` : '',
    preview.text.length || preview.returned.length ? 'Помощник перечитает возвращённые главы.' : '',
  ].filter(Boolean).join(' ');
  return { title: `Вернуть «${preview.bookTitle}» к версии до обновления?`, line, also };
}
