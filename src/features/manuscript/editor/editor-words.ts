import type { EditedDraftVersionSummary } from '../../../entities/manuscript/api';

/** The words of the editor: board 44·20, 44·21 (specs/2026-11-12-desk-boards.md, Data); what the board has not — Open questions. */
export const editorWords = {
  toChapter: 'К главе в книге',
  toBook: (title: string) => `К книге «${title}»`,
  title: 'Название главы',
  text: 'Текст главы',
  place: 'Место в книге',
  replace: 'Заменить главу в книге',
  add: 'Добавить в книгу',
  more: 'Действия главы',
  versions: 'Версии текста',
  revert: 'Вернуть как было',
  discardEdit: 'Отказаться от правки…',
  discardNew: 'Отказаться от главы…',
  saved: (time: string) => `Сохранено · ${time}`,
  unsaved: 'Не сохранено',
  same: 'Текст такой же, как в книге: заменять нечего.',
  changed: 'Глава в книге изменилась — откройте её заново.',
  changedLine: 'Глава в книге изменилась с тех пор, как вы её открыли',
  reopen: 'Открыть заново',
  empty: 'Сначала напишите текст главы.',
  close: 'Закрыть',
  // Not on the board (Open questions, 13.11): the nearest words of the site.
  gone: 'Главы больше нет в книге: её удалили.',
  replaced: 'Глава в книге обновлена',
  undo: 'Отменить',
  reverted: 'Прежний текст главы вернулся в книгу.',
  reopened: 'Текст главы взят из книги заново.',
  reopenedKept: 'Ваша правка — в «Версиях текста».',
  added: 'Глава добавлена в книгу',
  editDropped: 'Правка удалена',
  newDropped: 'Глава удалена',
  stays: 'Глава в книге останется.',
  revertQuestion: 'Вернуть как было?',
  revertSays: 'Глава в книге получит текст, который был до последней замены. Этот текст в поле тоже станет прежним.',
  allVersions: 'Все версии',
  takeVersion: 'Вернуть эту версию',
  takeKeeps: 'Нынешний текст останется в версиях',
  taken: 'Текст главы возвращён к версии',
  noVersions: 'Версий пока нет.',
  atStart: 'В начало книги',
  openFailed: 'Не открылась глава',
  versionsFailed: 'Не открылись версии главы',
  versionFailed: 'Не удалось загрузить версию.',
} as const;

/** «Отказаться от правки главы «Белый порт»?» — the question of «Отказаться…» (PA3.1). */
export function discardQuestion(kind: 'edit' | 'new', name: string) {
  const what = kind === 'edit' ? 'от правки главы' : 'от главы';
  return name ? `Отказаться ${what} «${name}»?` : `Отказаться ${what}?`;
}

/** «После главы 12 «Белый порт»» — a place in the book (board 44·21). */
export function afterChapterWords(chapter: { displayNumber: string; title: string }, chapterTitle: string) {
  return chapterTitle ? `После главы ${chapter.displayNumber} «${chapterTitle}»` : `После главы ${chapter.displayNumber}`;
}

function startOfDay(at: Date) {
  return new Date(at.getFullYear(), at.getMonth(), at.getDate());
}

/** «12:40» — the time on the author's clock. */
export function clockTime(at: Date) {
  return at.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
}

/** «Сегодня, 12:40», «Вчера, 18:20», «3 нояб., 09:15» — when a version was kept (board 44·20). */
export function versionTime(value: string, now = new Date()) {
  const at = new Date(value);
  const today = startOfDay(now);
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
  if (at >= today) return `Сегодня, ${clockTime(at)}`;
  if (at >= yesterday) return `Вчера, ${clockTime(at)}`;
  return `${at.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })}, ${clockTime(at)}`;
}

/**
 * What a version holds, in the words of the author (board 44·20): his text before «Открыть заново» or «Вернуть как было» —
 * «Ваша прежняя правка»; his text before another step — «Ваша правка»; with the first words of the place the step changed.
 */
export function versionWords(version: Pick<EditedDraftVersionSummary, 'reason' | 'place'>) {
  const who = version.reason === 'author_variant' ? 'Ваша прежняя правка' : 'Ваша правка';
  return version.place ? `${who}: «${version.place}»` : who;
}
