// SPDX-License-Identifier: AGPL-3.0-only
import type { SagaArchiveOpened, SagaFileSaveWords } from '../../../entities/saga-file/api';
import { formatCount, pluralize } from '../../../ui/copy';

/**
 * The words of the saga file of the app, word for word by board 44 (rows 44·2, 44·11, 44·30–44·32; specs/2026-11-13-desk-
 * boards3.md, the words of the card and of the chapters the helper does not know — specs/2026-11-13-desk-boards4.md BZ1.3).
 */
export const sagaFileWords = {
  section: 'Файл саги',
  save: 'Сохранить сагу в файл…',
  open: 'Открыть файл саги…',
  note: 'В файле — книги, всё, что помощник знает о саге, ваши записи и расшифровки чатов. Его можно открыть на другом компьютере',
  saving: 'Сохраняю сагу в файл',
  savingLong: 'До нескольких минут: в файл идут и ваши чаты',
  stop: 'Остановить',
  savingReason: 'Сага уже сохраняется',
  /** No saga yet: there is nothing to save (the board has no column for it; the decision log of the spec). */
  noSagaReason: 'Саги ещё нет — сохранять нечего',
  /** The list of sagas did not come to the press of «Сохранить сагу в файл…» (no column of the board; the decision log). */
  sagasUnread: 'Не удалось прочитать список саг',
  pick: 'Какую сагу сохранить в файл?',
  pickAction: 'Сохранить в файл',
  opening: (fileName: string) => `Открываю файл саги: ${fileName}`,
  openingLong: 'До минуты',
  openingReason: 'Файл уже открывается',
  opened: 'Сага открыта',
  goToSaga: 'Перейти к саге',
  card: 'Перенести сагу с сайта или другого компьютера',
  cardText: 'Сохраните сагу в файл там и откройте его здесь — книги и всё, что помощник знает о саге, переедут целиком',
  /** The general line of a refusal the page has no words of its own for; the code goes beside it. */
  openFailed: 'Не удалось открыть файл саги',
} as const;

/** «Сохранить сагу в файл» of the app: the file goes to «Загрузки» of the window (SW1.2; 44·30). */
export const saveWords: SagaFileSaveWords = {
  done: (fileName) => `Сага сохранена в файл: Загрузки → ${fileName}`,
  failed: 'Не удалось сохранить сагу в файл. Попробуйте ещё раз',
  busy: 'Сага уже сохраняется',
};

/**
 * A refusal of a file by its code (SW2.4; 44·32): one line, «Написать нам» with a file with an error in it. «Обновить» of a
 * file of a newer version comes with the part «Обновления» of «Настройки» (the decision log of the spec): the app has no way
 * to update itself yet, so the line says what to do.
 */
export const openRefusals: Readonly<Record<string, { message: string; write?: true }>> = {
  'saga_archive.not_archive': { message: 'Это не файл саги. Файл саги называется как сага и кончается на .cksaga' },
  'saga_archive.damaged': { message: 'Файл саги повреждён или скачан не до конца. Сохраните сагу в файл ещё раз и откройте новый' },
  'saga_archive.newer_version': { message: 'Этот файл сохранён более новой версией CanonKeeper. Обновите приложение и откройте файл снова' },
  'saga_archive.too_large': { message: 'Файл больше 1 ГБ — такой файл саги CanonKeeper не открывает' },
  'saga_archive.no_space': { message: 'На диске не хватает места, чтобы открыть файл саги. Освободите место и откройте файл снова' },
  'saga_archive.invalid': { message: 'В файле саги ошибка, и CanonKeeper не может его открыть', write: true },
};

/** The answer 429 of «Открыть файл саги»: a file of this author is being opened (SW2.4). */
export const openingAlready = 'Файл уже открывается';

/** «„Кто убил капитана“, „Хронология осады“»: titles as the board quotes them. */
const quoted = (titles: readonly string[]) => titles.map((title) => `„${title}“`).join(', ');

/** The verb by the number before it: «вошла 1 глава», «вошла 21 глава», «вошли 3 главы». */
const by = (count: number, one: string, many: string) => pluralize(count, one, many, many);
/** The pronoun of what was counted: «её» of one chapter, «их» of 21. */
const them = (count: number, one: string) => (count === 1 ? one : 'их');

/**
 * One note of the window «Сага открыта»: its words and the places it names. «Связать с аккаунтом» of the sections of the
 * workshops comes with the part «Аккаунт» of «Настройки» (the decision log of the spec): the open app links no account yet.
 */
export interface OpenedNote {
  key: string;
  text: string;
  places?: readonly string[];
}

/**
 * What the window «Сага открыта» says (SW2.3; 44·31): the first line names the saga by its new name with the numbers of
 * its books and chapters — that the helper knows it only when the file brought what he knows — and under it only the notes
 * the report gives a cause for, each in its own words, the numbers agreed with their nouns.
 */
export function openedWords({ name, renamed, report }: SagaArchiveOpened): { message: string; notes: OpenedNote[] } {
  const numbers = `${formatCount(report.books, 'книга', 'книги', 'книг')}, ${formatCount(report.chapters, 'глава', 'главы', 'глав')}`;
  const known = report.cards > 0 ? '. Помощник уже знает эту сагу — читать книги заново не нужно' : '';
  const notes: OpenedNote[] = [];
  if (renamed) notes.push({ key: 'name', text: `Сага с таким названием уже есть — эта названа „${name}“` });
  const unknown = report.chaptersWithoutCards;
  if (unknown.count > 0) {
    notes.push({
      key: 'unknown', places: unknown.places,
      text: `Помощник ещё не знает ${formatCount(unknown.count, 'главу', 'главы', 'глав')}: он прочитает ${them(unknown.count, 'её')}, когда вы подключите помощника`,
    });
  }
  const chats = report.chatsNotRead;
  if (chats.length > 0) {
    notes.push({
      key: 'chats',
      text: `${by(chats.length, 'Не перенёсся', 'Не перенеслись')} ${formatCount(chats.length, 'чат', 'чата', 'чатов')}: ${quoted(chats)} — ${them(chats.length, 'его')} не удалось прочитать, когда сага сохранялась в файл`,
    });
  }
  const covers = report.coversSkipped;
  if (covers.length > 0) {
    notes.push({ key: 'covers', text: covers.length === 1 ? `Обложка книги ${quoted(covers)} не перенеслась` : `Обложки книг ${quoted(covers)} не перенеслись` });
  }
  const { drafts, drawings, trashedBooks, trashedChapters, othersTurns } = report.leftOut;
  const leftHere = [
    drafts > 0 ? formatCount(drafts, 'глава в работе', 'главы в работе', 'глав в работе') : null,
    drawings > 0 ? formatCount(drawings, 'рисунок', 'рисунка', 'рисунков') : null,
  ].filter((part): part is string => part !== null);
  if (leftHere.length > 0) {
    const alone = leftHere.length === 1 ? (drafts || drawings) : 0;
    const verb = alone ? by(alone, drafts ? 'вошла' : 'вошёл', 'вошли') : 'вошли';
    const stayed = alone === 1 ? (drafts ? 'она осталась' : 'он остался') : 'они остались';
    notes.push({ key: 'workshops', text: `В файл не ${verb} ${leftHere.join(' и ')} — ${stayed} там, где сага сохранялась` });
  }
  const trashed = [
    trashedBooks > 0 ? formatCount(trashedBooks, 'книга', 'книги', 'книг') : null,
    trashedChapters > 0 ? formatCount(trashedChapters, 'глава', 'главы', 'глав') : null,
  ].filter((part): part is string => part !== null);
  if (trashed.length > 0) {
    const alone = trashed.length === 1 ? (trashedBooks || trashedChapters) : 0;
    notes.push({ key: 'trash', text: `В файл не ${alone ? by(alone, 'вошла', 'вошли') : 'вошли'} ${trashed.join(' и ')} из „Недавно удалённых“` });
  }
  if (othersTurns > 0) {
    notes.push({ key: 'others', text: `В файл не ${by(othersTurns, 'вошёл', 'вошли')} ${formatCount(othersTurns, 'вопрос', 'вопроса', 'вопросов')} других участников саги` });
  }
  if (report.sections.length > 0) {
    notes.push({ key: 'sections', text: 'В файле есть главы в работе и рисунки. Свяжите приложение с аккаунтом, чтобы открыть их' });
  }
  return { message: `${name}: ${numbers}${known}`, notes };
}
