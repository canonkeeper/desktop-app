import { pluralize } from '../../ui/copy';
import { isApiStatusFailure } from '../api-errors';
import type { AccountQuota, QuotaCounter } from './api';

/**
 * A refusal by a limit in the words of the work (specs/2026-11-10-ux2-patterns-refusals.md PR2.1, PR2.3; П-12): what
 * ended and when it comes back or what stays possible meanwhile. One table by the code of the refusal for every section;
 * the numbers are those of the counter beside it (`getAccountQuota`), so the block and the counter cannot disagree. What
 * the next tier gives is the third part, said by the build of the site.
 */
export interface RefusalWords {
  /** «Вопросы на сегодня закончились — 5 из 5». */
  ended: string;
  /** «Новые — после 00:00 по Москве.», «Пробные не возвращаются. …». */
  returns: string;
  /**
   * The refusal ends by itself in minutes and no tier or letter changes it — the answers being prepared (AA2.7, Ч-36):
   * the block has no «Написать нам».
   */
  passes?: true;
}

/** Characters as the author reads them: «15 млн», «12,4 млн», «800». */
export function formatCharacters(value: number) {
  if (value < 1_000_000) return value.toLocaleString('ru-RU');
  const millions = Math.floor(value / 100_000) / 10;
  return `${millions.toLocaleString('ru-RU', { maximumFractionDigits: 1 })} млн`;
}

const midnight = 'Новые — после 00:00 по Москве.';
const otherSaga = 'Книги саги на месте. Новую книгу можно загрузить в другую сагу.';
const ownBooks = 'Ваши книги на месте. Новые поместятся, когда книг станет меньше.';

/**
 * « — 5 из 5» by the counter of the account; nothing when the counter is not read, or not yet read again after the
 * refusal and still shows something left: «закончились — 4 из 5» would contradict itself.
 */
function count(counter: QuotaCounter | undefined, after = '') {
  return counter && counter.limit > 0 && counter.used >= counter.limit ? ` — ${counter.used} из ${counter.limit}${after}` : '';
}

/** The limits of a saga are the same for every saga of the author: the first one he owns names them. */
function sagaSources(quota: AccountQuota | undefined) {
  return quota?.ownedProjects[0]?.sources;
}

type Words = (quota: AccountQuota | undefined, counter?: QuotaCounter) => RefusalWords;

const table: Record<string, Words> = {
  // A correction of a running answer spends the same unit as a question (specs/2026-11-09-quota-accounting.md Q2.1).
  'quota.chat_turns_daily': (quota) => ({ ended: `Вопросы и уточнения на сегодня закончились${count(quota?.chatTurns)}`, returns: midnight }),
  'quota.active_turns': (quota) => {
    const limit = quota?.activeTurns.limit;
    return {
      ended: limit === undefined ? 'Сейчас готовятся ответы на ваши вопросы'
        : `Сейчас ${pluralize(limit, 'готовится ответ', 'готовятся ответы', 'готовятся ответы')} на ${limit} ${pluralize(limit, 'ваш вопрос', 'ваших вопроса', 'ваших вопросов')}`,
      returns: `Новый вопрос можно задать, когда ${limit === 1 ? 'он' : 'один из них'} будет готов.`,
      passes: true,
    };
  },
  'quota.imports_daily': (quota) => ({ ended: `Загрузки книг на сегодня закончились${count(quota?.imports)}`, returns: midnight }),
  'quota.projects': (quota) => ({
    ended: `Место для новых саг закончилось${count(quota?.projects)}`,
    returns: 'Ваши саги на месте. Новая поместится, когда саг станет меньше.',
  }),
  // The books count in every saga (specs/2026-11-11-tier-limits.md TL4.1): the counter of this saga — given by the place,
  // or the one saga the author owns — says how many; else every saga of his has the same limit, so the words name it.
  'quota.project_books': (quota, given) => {
    const counter = given ?? (quota?.ownedProjects.length === 1 ? quota.ownedProjects[0]!.sources.books : undefined);
    if (counter && counter.limit > 0) {
      return {
        ended: counter.used >= counter.limit ? `В этой саге уже ${counter.used} ${pluralize(counter.used, 'книга', 'книги', 'книг')} из ${counter.limit}`
          : `Книги не поместились: в этой саге стало бы больше ${counter.limit} ${pluralize(counter.limit, 'книги', 'книг', 'книг')}`,
        returns: otherSaga,
      };
    }
    const books = sagaSources(quota)?.books.limit;
    return { ended: books === undefined ? 'В этой саге больше нет места для книг' : `Книги не поместились: в одной саге — до ${books} ${pluralize(books, 'книги', 'книг', 'книг')}`, returns: otherSaga };
  },
  'quota.project_chapters': (quota) => {
    const chapters = sagaSources(quota)?.chapters.limit;
    return { ended: chapters === undefined ? 'Книга не поместилась: в саге больше нет места для глав' : `Книга не поместилась: в саге стало бы больше ${chapters} ${pluralize(chapters, 'главы', 'глав', 'глав')}`, returns: otherSaga };
  },
  'quota.project_characters': (quota) => {
    const characters = sagaSources(quota)?.characters.limit;
    return { ended: characters === undefined ? 'Книга не поместилась: в саге больше нет места для текста' : `Книга не поместилась: текст саги стал бы больше ${formatCharacters(characters)} знаков`, returns: otherSaga };
  },
  'quota.account_books': (quota) => {
    const books = quota?.account.books;
    // A few books at once may not fit while some room is left: the refusal says so by the number they would pass.
    const room = !books ? 'Книги не поместились: во всех сагах больше нет места для книг'
      : books.used < books.limit ? `Книги не поместились: во всех сагах стало бы больше ${books.limit} ${pluralize(books.limit, 'книги', 'книг', 'книг')}`
        : `Место для книг закончилось${count(books, ' во всех сагах')}`;
    return { ended: room, returns: ownBooks };
  },
  'quota.account_chapters': (quota) => {
    const chapters = quota?.account.chapters.limit;
    return { ended: chapters === undefined ? 'Книга не поместилась: во всех сагах больше нет места для глав' : `Книга не поместилась: во всех сагах стало бы больше ${chapters} ${pluralize(chapters, 'главы', 'глав', 'глав')}`, returns: ownBooks };
  },
  'quota.account_characters': (quota) => {
    const characters = quota?.account.characters.limit;
    return { ended: characters === undefined ? 'Книга не поместилась: во всех сагах больше нет места для текста' : `Книга не поместилась: текст всех саг стал бы больше ${formatCharacters(characters)} знаков`, returns: ownBooks };
  },
  'quota.images_daily': (quota, counter) => ({ ended: `Рисунки на сегодня закончились${count(counter ?? quota?.images)}`, returns: `${midnight} Описание можно подготовить уже сейчас.` }),
  'quota.image_uploads_daily': (quota) => ({ ended: `Картинки на сегодня закончились${count(quota?.imageUploads)}`, returns: `${midnight} Загруженные остаются в «Моих картинках».` }),
  'quota.drawing_turns_daily': (quota) => ({ ended: `Сообщения помощнику на сегодня закончились${count(quota?.drawingTurns)}`, returns: `${midnight} Можно нарисовать без помощника: ровно по вашим словам.` }),
  'quota.chapter_drafts_daily': (quota) => ({ ended: `Письмо, проверка и вычитка глав на сегодня закончились${count(quota?.chapterDrafts)}`, returns: `${midnight} Писать и править самому можно всегда.` }),
  'quota.draft_turns_daily': (quota) => ({ ended: `Сообщения помощнику письма на сегодня закончились${count(quota?.draftTurns)}`, returns: `${midnight} Писать и править самому можно всегда.` }),
  'quota.draft_rewrites_daily': (quota) => ({ ended: `Варианты места на сегодня закончились${count(quota?.draftRewrites)}`, returns: `${midnight} Править текст самому можно всегда.` }),
  // The voice of the site (specs/2026-11-08-subscription-tiers.md TR8.6): the notices of its call by the tier.
  // The page of the conversation says them with its numbers and its place (specs/2026-11-10-ux5-voice.md VO4.1, VO5.2).
  'voice.minutes': () => ({ ended: 'Время разговора закончилось', returns: 'Записанное за разговор осталось на месте.' }),
  'voice.minutes_today': () => ({ ended: 'Минуты разговора на сегодня закончились', returns: midnight }),
  'voice.plan': () => ({ ended: 'Пробные разговоры закончились', returns: 'Пробные не возвращаются. Спрашивать и писать текстом можно всегда.' }),
  // No trial drawing left (specs/2026-11-08-subscription-tiers.md TR6.1): the drawings of a day are on the tiers above.
  'illustrations.locked': () => ({
    ended: 'Пробные рисунки закончились',
    returns: 'Пробные не возвращаются. Смотреть, скачивать, делать образцом и обложкой можно.',
  }),
};

/**
 * The words of a refusal by a limit; null for a refusal of another kind. `counter` is the counter the place shows beside
 * the block when it is not the account's — the drawings of the day of «Рисование» read with the right to draw.
 */
export function refusalWords(code: string | null | undefined, quota: AccountQuota | undefined, counter?: QuotaCounter): RefusalWords | null {
  const words = code ? table[code] : undefined;
  return words ? words(quota, counter) : null;
}

/** A failure of a request that is a refusal by a limit: its code and words for the one block; null for any other. */
export function limitRefusalOf(error: unknown, quota?: AccountQuota, counter?: QuotaCounter) {
  const code = isApiStatusFailure(error) ? error.problem.code : undefined;
  const words = refusalWords(code, quota, counter);
  return code && words ? { code, words } : null;
}

/** The same words in one line, where a line is all a place has: a row of the upload, a line of a window. */
export function refusalLine(code: string | null | undefined, quota: AccountQuota | undefined) {
  const words = refusalWords(code, quota);
  return words ? `${words.ended}. ${words.returns}` : undefined;
}
