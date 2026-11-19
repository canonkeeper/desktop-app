// SPDX-License-Identifier: AGPL-3.0-only
import { formatCount } from '../../../ui/copy';
import { aboutTimeWords, amountWords, approximately, millionsWords, tokensWords, tookTimeWords, type ReadingUnit } from './amounts';
import type { ReadingEstimate, ReadingSample } from './reading-api';

/**
 * The words of the windows of the reading of the books by «Свой ИИ» (specs/2026-11-13-desk-consent-web.md RW1.4, RW2; boards
 * 44·27, 44·42; the table Data of specs/2026-11-13-desk-consent.md). Every number is of the answers of the server.
 */
export const consentWords = {
  title: 'Прочитать книги своим ИИ?',
  rough: (chapters: number) => `Оценка грубая. После пробы на ${chapters === 3 ? 'трёх главах' : formatCount(chapters, 'главе', 'главах', 'главах')} она станет точной`,
  noPrices: 'Укажите цены в «Дополнительно», чтобы видеть сумму',
  stop: 'Остановиться, когда потрачено',
  stopHint: 'Чтение встанет на паузу и спросит вас',
  notNow: 'Не сейчас',
  readAll: 'Прочитать всё',
  readRest: 'Прочитать остальное',
  notRead: 'Не читать',
  /** «Свести имена и нити», «Свести имена», «Свести нити» — by what is left (specs/2026-11-15-desk-reading-rest.md RR2.7). */
  namesOnly: (names: boolean, threads: boolean) => `Свести ${restWords(names, threads, 'short')}`,
  sample: (chapters: number) => `Попробовать на ${formatCount(chapters, 'главе', 'главах', 'главах')}`,
  again: 'Попробовать ещё раз',
  stopSample: 'Остановить',
  sampleLeave: 'можно закрыть окно — проба продолжится',
  settings: 'Открыть настройки помощника',
  summary: 'Пересказ',
  events: 'События',
  people: 'Кто в главе',
  noSumTitle: 'Читать без суммы остановки?',
  noSumAction: 'Читать без суммы',
  notANumber: (unit: ReadingUnit) => (unit === 'tokens' ? 'Напишите, сколько миллионов токенов, — например 25' : 'Напишите сумму числом — например 1 000'),
  loadFailed: 'Не открылась оценка чтения книг',
  nothingToRead: 'Читать нечего: все главы прочитаны, имена героев и сюжетные нити сведены',
  close: 'Закрыть',
  started: 'Помощник читает книги',
  startedWithout: 'Без суммы остановки — остановить чтение можно кнопкой «Пауза»',
  startedWith: (stop: string) => `Чтение встанет на паузу, когда будет потрачено ${stop}`,
} as const;

/**
 * What is left to bring together once the chapters are read (specs/2026-11-15-desk-reading-rest.md RR2.7) — «имена героев и
 * сюжетные нити», «имена героев» or «сюжетные нити»; short, for the button of the window of the consent, «имена и нити»,
 * «имена» or «нити» — by the two signs of the server: the one function of the line of the reading, its pause, its wait for the
 * consent, the window of the consent and its button, and the window without a sum.
 */
export function restWords(names: boolean, threads: boolean, form: 'full' | 'short' = 'full') {
  const [heroes, plot] = form === 'short' ? ['имена', 'нити'] : ['имена героев', 'сюжетные нити'];
  if (names && !threads) return heroes;
  if (threads && !names) return plot;
  return `${heroes} и ${plot}`;
}

/**
 * What the helper does and where the text goes (RW2.1): the host of the provider, whose rules decide whether it learns on the
 * texts and keeps them (specs/2026-11-19-desk-trust.md TW1.4), or the program on this computer.
 */
export function consentMessage(estimate: ReadingEstimate) {
  const where = estimate.local
    ? `Текст книг получит программа на этом компьютере (${estimate.host}) — не провайдер в сети.`
    : `Текст книг уйдёт вашему провайдеру ${estimate.host}. Учится ли провайдер на ваших текстах и хранит ли их, решают его правила — проверьте их до чтения`;
  if (estimate.chapters === 0) {
    return estimate.namesWait || estimate.threadsWait
      ? `Главы прочитаны. Помощнику осталось свести ${restWords(estimate.namesWait, estimate.threadsWait)}. ${where}`
      : consentWords.nothingToRead;
  }
  return `Помощник прочитает ${formatCount(estimate.chapters, 'главу', 'главы', 'глав')} и узнает, кто есть кто, что произошло и где это в книге. ${where}`;
}

/** «Оценка: ≈ 14 млн токенов на вход и ≈ 5 млн на выход · ≈ 680 ₽ · около 5 часов»; after a sample — «Теперь оценка: …». */
export function estimateLine(estimate: ReadingEstimate, afterSample: boolean) {
  const sum = estimate.amount === null ? [] : [`≈ ${amountWords(estimate.amount, estimate.unit)}`];
  const tokens = `≈ ${tokensWords(estimate.inputTokens)} на вход и ≈ ${millionsWords(estimate.outputTokens)} на выход`;
  return `${afterSample ? 'Теперь оценка' : 'Оценка'}: ${[tokens, ...sum, aboutTimeWords(estimate.seconds)].join(' · ')}`;
}

/** What a sample spent: in the currency of the settings, or tokens in and out without prices; «≈» with an estimate in it. */
function sampleSpent(sample: ReadingSample) {
  const { amount, approximate, inputTokens, outputTokens, unit } = sample.spent;
  return approximately(amount === null ? tokensWords(inputTokens + outputTokens) : amountWords(amount, unit), approximate);
}

/** «Читаю 3 главы: 1 из 3» — the line of the work of a sample (board 44·27 «Проба идёт»). */
export function sampleGoingLine(sample: ReadingSample) {
  return `Читаю ${formatCount(sample.total, 'главу', 'главы', 'глав')}: ${sample.done} из ${sample.total}`;
}

/** The line of the reading while a sample runs: the shelf, the chat and the map say it too (RW2.2). */
export function sampleLine(sample: ReadingSample) {
  return `Помощник читает ${formatCount(sample.total, 'главу', 'главы', 'глав')} на пробу: ${sample.done} из ${sample.total}`;
}

/** «Проба готова: 3 главы за 4 минуты · потрачено ≈ 4 ₽», «Проба остановлена: прочитано 1 из 3 глав · потрачено ≈ 1 ₽». */
export function sampleResult(sample: ReadingSample) {
  if (sample.state === 'canceled') {
    return `Проба остановлена: прочитано ${sample.done} из ${formatCount(sample.total, 'главы', 'глав', 'глав')} · потрачено ${sampleSpent(sample)}`;
  }
  return `Проба готова: ${formatCount(sample.total, 'глава', 'главы', 'глав')} ${tookTimeWords(sample.seconds)} · потрачено ${sampleSpent(sample)}`;
}

type Failure = NonNullable<ReadingSample['failure']>;

/** «Проба не получилась: …» — the reason in the words of the pauses of the reading (Data), and whether the settings help. */
export const sampleFailures: Record<Failure, { words: string; settings: boolean }> = {
  funds: { words: 'провайдер отказал — кончились средства на счёте или он пока не принимает запросы', settings: false },
  key: { words: 'провайдер не принял ключ', settings: true },
  model: { words: 'у провайдера нет модели с таким названием', settings: true },
  connection: { words: 'нет связи с провайдером', settings: false },
  paused: { words: 'чтение остановилось, не дочитав главы пробы', settings: false },
  helper: { words: 'помощник не подключён', settings: true },
  other: { words: 'ответ помощника не пришёл', settings: false },
};

export function sampleFailureLine(failure: ReadingSample['failure']) {
  return `Проба не получилась: ${sampleFailures[failure ?? 'other']?.words ?? sampleFailures.other.words}`;
}

/** «Помощник прочитает все 612 глав и книги, которые вы добавите позже, — примерно на 690 ₽…» (board 44·27 «Без суммы»). */
export function noSumMessage(estimate: ReadingEstimate) {
  const about = estimate.amount === null ? tokensWords(estimate.inputTokens + estimate.outputTokens) : amountWords(estimate.amount, estimate.unit);
  const what = estimate.chapters > 0
    ? `прочитает все ${formatCount(estimate.chapters, 'главу', 'главы', 'глав')} и книги, которые вы добавите позже`
    : `сведёт ${restWords(estimate.namesWait, estimate.threadsWait)} и прочитает книги, которые вы добавите позже`;
  return `Помощник ${what}, — примерно на ${about}. Остановить чтение можно кнопкой «Пауза»`;
}

/** The window «Сумма остановки» (board 44·42). */
export const sumWords = {
  title: 'Сумма остановки',
  spent: (words: string) => `Потрачено ${words}`,
  noSum: 'Без суммы — читать до конца',
  cancel: 'Отмена',
  save: 'Сохранить',
  saveAndGo: 'Сохранить и продолжить',
  small: (spent: string) => `Сумма должна быть больше уже потраченного: ${spent}`,
  empty: 'Напишите сумму или выберите «Без суммы — читать до конца»',
  goes: (stop: string) => `Чтение встанет на паузу, когда будет потрачено ${stop}`,
  goesWithout: 'Чтение пойдёт до конца, без суммы остановки',
  continued: 'Чтение продолжается',
  saved: 'Сумма остановки сохранена',
  /** The new unit after a change of the prices is not read yet. */
  unitPending: 'Читаю, в чём теперь считается сумма…',
} as const;

export type UnreadReason = 'cut_answer' | 'unreadable' | 'refused' | 'no_answer';

/**
 * Why what did not read or come together did not, in the words of «Свой ИИ» with what helps (Data of
 * specs/2026-11-15-desk-reading-rest.md). The neutral words of the helper of the account are its block's
 * (specs/2026-11-15-desk-account-words-web.md AW5.4).
 */
export const unreadReasonWords: Record<UnreadReason, string> = {
  cut_answer: 'ответ помощника оборвался — увеличьте «Предел ответа»',
  refused: 'провайдер не принял запрос — попробуйте другую модель для чтения книг',
  no_answer: 'провайдер не ответил',
  unreadable: 'ответ помощника не удалось разобрать',
};

/** The one reason of the line: the first of what is marked in this order (Data). */
export const unreadReasonOrder: readonly UnreadReason[] = ['cut_answer', 'refused', 'no_answer', 'unreadable'];

/** What it means for the author, by what did not come together; the chapters alone have no tail (Data). */
export const unreadMeaningWords = {
  names: 'Спрашивать можно — героя под другим именем помощник может не узнать',
  threads: 'Спрашивать можно — о незакрытых нитях помощник ответит хуже',
  both: 'Спрашивать можно — ответы будут менее точными',
  withChapters: 'Спрашивать можно — помощник ответит по прочитанному',
} as const;

/** «Повторить» and its end (Data): the success, the refusal, and why the button waits for a new sum. */
export const repeatWords = {
  started: 'Помощник пробует снова',
  refused: 'Повтор не начался',
  spent: 'Сумма остановки уже потрачена — назовите новую в «Изменить сумму…»',
} as const;

/** The refusals of the writes of the reading in the words of the author (specs/2026-11-13-desk-consent.md: the codes). */
export const refusalWords: Record<string, string> = {
  'helper.not_connected': 'Помощник не подключён — подключите его в настройках помощника.',
  'reading.no_consent': 'У этой саги нет согласия на чтение — нажмите «Прочитать книги…».',
  'reading.consent_given': 'Чтение книг уже идёт.',
  'reading.nothing_to_read': 'Читать нечего: все главы прочитаны.',
  'reading.invalid': 'Сумма остановки уже потрачена — назовите новую в «Изменить сумму…».',
};
