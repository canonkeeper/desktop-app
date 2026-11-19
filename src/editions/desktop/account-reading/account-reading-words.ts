// SPDX-License-Identifier: AGPL-3.0-only
import { formatCount } from '../../../ui/copy';
import { aboutTimeWords } from '../reading/amounts';
import type { ReadingEstimate } from '../reading/reading-api';
import type { UnreadReason } from '../reading/reading-words';
import { restWords } from '../reading/reading-words';
import { charactersRange, charactersWords } from './characters';

/**
 * The words of the reading of the books by the helper of the account (specs/2026-11-15-desk-account-words-web.md AW5, Data;
 * boards 44·43, 44·44): its own block — no provider, no tokens, no sum, no prices, no sample. Every number is of the answers of
 * the server; what is brought together is the one function of the names and the threads (`restWords`, RR2.7).
 */
export const accountReadingWords = {
  // The buttons of the line.
  consent: 'Прочитать книги…',
  pause: 'Пауза',
  continue: 'Продолжить',
  retry: 'Повторить',
  // The window of the consent.
  title: 'Прочитать книги с помощником CanonKeeper?',
  readBooks: 'Прочитать книги',
  notNow: 'Не сейчас',
  close: 'Закрыть',
  nothingToRead: 'Читать нечего: все главы прочитаны, имена героев и сюжетные нити сведены',
  workShort: 'На тарифе не хватит работы помощника на все книги за 30 дней. Помощник прочитает, сколько поместится',
  loadFailed: 'Не открылась оценка чтения книг',
  started: 'Помощник читает книги',
  startedDetail: 'Остановить чтение можно кнопкой «Пауза»',
  linkBroken: 'Связь с аккаунтом прервана',
  notStarted: 'Чтение не началось. Попробуйте ещё раз.',
  // The writes of the line.
  notPaused: 'Чтение не встало на паузу',
  notContinued: 'Чтение не продолжилось',
  retryStarted: 'Помощник пробует снова',
  retryRefused: 'Повтор не начался',
  tryAgain: 'Попробуйте ещё раз.',
} as const;

/** «Свести имена и нити», «Свести имена», «Свести нити» — the main button when only those are left (RR2.7). */
export function bringTogetherWords(names: boolean, threads: boolean) {
  return `Свести ${restWords(names, threads, 'short')}`;
}

/** With whom the helper of the account works, after «…к помощнику» of both kinds of the consent (specs/2026-11-19-desk-trust.md TW1.2). */
const providerTail = ' — он работает у OpenAI (ChatGPT) или другого крупного облачного поставщика, обучение на ваших текстах выключено';

/** What the helper does and where the text goes (AW5.2): it reads the chapters, or only brings the names and the threads together. */
export function accountConsentMessage(estimate: Pick<ReadingEstimate, 'chapters' | 'namesWait' | 'threadsWait'>) {
  if (estimate.chapters === 0) {
    return estimate.namesWait || estimate.threadsWait
      ? `Главы прочитаны. Помощнику осталось свести ${restWords(estimate.namesWait, estimate.threadsWait)}. Текст пройдёт через сервер CanonKeeper к помощнику${providerTail}`
      : accountReadingWords.nothingToRead;
  }
  return `Помощник прочитает ${formatCount(estimate.chapters, 'главу', 'главы', 'глав')} и узнает, кто есть кто, что произошло и где это в книге. `
    + `Текст книг пройдёт через сервер CanonKeeper к помощнику${providerTail}. Книги, которые вы добавите позже, помощник прочитает так же — остановить `
    + 'можно кнопкой «Пауза»';
}

type AccountEstimate = NonNullable<ReadingEstimate['account']>;

/**
 * «Это займёт до 6,4 млн знаков тарифа · около 11 часов. За 30 дней использовано 3,2 из 40 млн» — what the reading takes, rounded
 * up, and what was used of the tier, down; the limits the site has not told yet — the volume alone.
 */
export function volumeLine(estimate: Pick<ReadingEstimate, 'seconds'>, account: AccountEstimate) {
  const takes = `Это займёт до ${charactersWords(account.characters, 'up')} знаков тарифа · ${aboutTimeWords(estimate.seconds)}`;
  const limit = account.charactersLimit;
  return limit ? `${takes}. За 30 дней использовано ${charactersRange(limit.used, limit.limit)}` : takes;
}

/** Which limit ends first, by the server (`endsFirst`): the characters with the numbers of the server, the work without numbers. */
export function endsFirstLine(account: AccountEstimate): string | null {
  const limit = account.charactersLimit;
  if (account.endsFirst === 'characters' && limit) {
    return `За 30 дней использовано ${charactersRange(limit.used, limit.limit)} знаков тарифа, а чтение займёт до ${charactersWords(account.characters, 'up')}. Помощник прочитает, сколько поместится`;
  }
  return account.endsFirst === 'work' ? accountReadingWords.workShort : null;
}

/**
 * Why the names or the threads did not come together, in the neutral words of the helper of the account — no provider and no
 * settings to open (Data of specs/2026-11-15-desk-reading-rest.md, RR2.6; AW5.4).
 */
export const accountUnreadReasonWords: Record<UnreadReason, string> = {
  cut_answer: 'ответ помощника оборвался',
  refused: 'запрос не принят',
  no_answer: 'помощник не ответил',
  unreadable: 'ответ помощника не удалось разобрать',
};

/** The refusals of the writes of the reading through the account in the words of the author: no sum, no settings of a provider. */
export const accountRefusalWords: Record<string, string> = {
  'helper.not_connected': 'Связь с аккаунтом прервана.',
  'reading.no_consent': 'У этой саги нет согласия на чтение — нажмите «Прочитать книги…».',
  'reading.consent_given': 'Чтение книг уже идёт.',
  'reading.nothing_to_read': 'Читать нечего: все главы прочитаны.',
};
