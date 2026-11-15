// SPDX-License-Identifier: AGPL-3.0-only
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { askWords, chaptersOf, waitingLine } from '../../../entities/manuscript/preparation-copy';
import { formatCount } from '../../../ui/copy';
import type { HelperReason } from '../account/helper-reason';
import { aboutTimeWords, amountWords, approximately, rangeWords } from './amounts';
import { repeatWords, restWords, unreadMeaningWords, unreadReasonOrder, unreadReasonWords, type UnreadReason } from './reading-words';

export type Reading = NonNullable<IndexingStatus['reading']>;
type Pause = NonNullable<Reading['pause']>;

/**
 * What a button of the line does (specs/2026-11-13-desk-consent-web.md RW1.3): one operation of the consent with its fields by
 * the table of the server (specs/2026-11-13-desk-consent.md CN4.1), a window, or the way to the settings of the helper.
 */
export type ReadingAction =
  | { kind: 'connect'; label: string }
  | { kind: 'relink'; label: string }
  | { kind: 'linkWindow'; label: string }
  | { kind: 'consent'; label: string }
  | { kind: 'pause'; label: string }
  | { kind: 'continue'; label: string }
  | { kind: 'continueBy'; label: string; step: number }
  | { kind: 'sum'; label: string }
  | { kind: 'settings'; label: string }
  /** «Повторить»: unavailable with the reason while the stop sum is spent (specs/2026-11-15-desk-reading-rest.md RR2.4). */
  | { kind: 'retryUnread'; label: string; disabledReason?: string };

/** One line of the reading in words: what the slot of the edition draws (`PreparationLine`) before its buttons are made. */
export interface ReadingLineWords {
  label: string;
  title: string;
  /** In their order; the main one last. */
  actions: ReadingAction[];
  ask: boolean;
  tone: 'work' | 'stalled' | 'done';
  progress: number | null;
  /** The line stands under the line of the state of the place, which keeps its words when nothing takes their place. */
  under: boolean;
}

/** The words of the buttons of the line (boards 44·3, 44·27, 44·28). */
export const readingButtons = {
  connect: 'Подключить помощника',
  relink: 'Связать снова',
  linkWindow: 'Открыть окно связи',
  consent: 'Прочитать книги…',
  pause: 'Пауза',
  continue: 'Продолжить',
  retry: 'Повторить',
  sum: 'Изменить сумму…',
  settings: 'Открыть настройки помощника',
} as const;

/**
 * «Главы прочитаны — …»: what is left once every chapter is read and what is brought together while it goes (Data of
 * specs/2026-11-13-desk-consent.md, board 44·28; specs/2026-11-15-desk-reading-rest.md RR2.7) — the names, the threads or both,
 * as the server says them.
 */
const restLeft = (reading: Reading) => `Главы прочитаны — помощнику осталось свести ${restWords(reading.namesWait, reading.threadsWait)}`;
const restGoing = (reading: Reading) => `Главы прочитаны — помощник сводит ${restWords(reading.namesWait, reading.threadsWait)}`;

/**
 * The pauses of the reading by «Свой ИИ». The pauses of the helper of the account (specs/2026-11-13-desk-account-ai.md AI3.5) are
 * said by its block (specs/2026-11-15-desk-account-words-web.md AW5.3): the slot gives this block no reading of the account.
 */
type OwnPause = Exclude<Pause, 'unavailable' | 'unfit' | 'characters' | 'reading_fence'>;

/** Why the reading stands (board 44·28 and the table Data): the first sentence of the line of a pause. */
const pauseWords: Record<OwnPause, (reading: Reading) => string> = {
  author: () => 'Чтение на паузе по вашей просьбе',
  sum: (reading) => (reading.stopAt !== null && reading.spent >= reading.stopAt
    ? `Чтение на паузе: потрачено ${approximately(amountWords(reading.spent, reading.unit), reading.approximate)} — сумма, на которой вы просили остановиться`
    : `Чтение на паузе: следующий запрос превысил бы сумму, на которой вы просили остановиться. Потрачено ${spentWords(reading)}`),
  prices: () => 'Чтение на паузе: цены или валюта в настройках изменились. Назовите сумму остановки заново',
  funds: () => 'Чтение на паузе: провайдер отказал — кончились средства на счёте или он пока не принимает запросы',
  key: () => 'Чтение на паузе: провайдер не принял ключ',
  model: () => 'Чтение на паузе: у провайдера нет модели с таким названием',
  connection: () => 'Чтение на паузе: нет связи с провайдером',
  // specs/2026-11-15-desk-reading-no-answer.md NA1.3: the provider did not answer a request through its repeats.
  no_answer: () => 'Чтение на паузе: провайдер не отвечает',
};

/** «≈ 240 из 1 000 ₽», «4 из 25 млн токенов»; without a stop sum only what was spent: «≈ 240 ₽». */
function spentWords(reading: Reading) {
  const words = reading.stopAt === null ? amountWords(reading.spent, reading.unit) : rangeWords(reading.spent, reading.stopAt, reading.unit);
  return approximately(words, reading.approximate);
}

/** The buttons of a pause, by its reason (board 44·28): each pause has its way out. */
function pauseActions(reading: Reading): ReadingAction[] {
  switch (reading.pause) {
    case 'sum':
      // «Продолжить ещё на X» names X the server keeps — the sum the author named last — never one of the page's memory.
      return [
        { kind: 'sum', label: readingButtons.sum },
        ...(reading.step !== null ? [{ kind: 'continueBy' as const, label: `Продолжить ещё на ${amountWords(reading.step, reading.unit)}`, step: reading.step }] : []),
      ];
    case 'prices':
      return [{ kind: 'sum', label: readingButtons.sum }];
    case 'key':
    case 'model':
      return [{ kind: 'settings', label: readingButtons.settings }];
    case 'connection':
    case 'no_answer':
      return [{ kind: 'continue', label: readingButtons.retry }];
    default:
      return [{ kind: 'continue', label: readingButtons.continue }];
  }
}

/**
 * All that did not read and did not come together in words (specs/2026-11-15-desk-reading-rest.md RR2.3, Data): «<что>:
 * <причина>. <что это значит>» — the reason the first of what is marked in the order of Data, in the words of the block that
 * says it: «Свой ИИ» here, the helper of the account in its own block (specs/2026-11-15-desk-account-words-web.md AW5.4). Null —
 * nothing is marked.
 */
export function unreadSaying(reading: Reading, reasons: Record<UnreadReason, string>): { label: string; title: string; reason: UnreadReason } | null {
  const chapters = reading.unread;
  const names = reading.unreadRest?.names ?? null;
  const threads = reading.unreadRest?.threads ?? null;
  const marked = [chapters?.reason, names, threads];
  const reason = unreadReasonOrder.find((each) => marked.includes(each));
  if (!reason) return null;
  const rest = names || threads ? `${restWords(Boolean(names), Boolean(threads))} не сведены` : null;
  let what: string;
  let meaning: string | null;
  if (chapters) {
    const count = formatCount(chapters.chapters, 'глава', 'главы', 'глав');
    const verb = chapters.chapters % 10 === 1 && chapters.chapters % 100 !== 11 ? 'не прочиталась' : 'не прочитались';
    what = rest ? `${count} ${verb}, ${rest}` : `${count} ${verb}`;
    meaning = rest ? unreadMeaningWords.withChapters : null;
  } else {
    what = `${rest!.charAt(0).toUpperCase()}${rest!.slice(1)}`;
    meaning = names && threads ? unreadMeaningWords.both : names ? unreadMeaningWords.names : unreadMeaningWords.threads;
  }
  const why = reasons[reason];
  return { label: what, title: meaning ? `${what}: ${why}. ${meaning}` : `${what}: ${why}`, reason };
}

/**
 * The line of all that did not read and did not come together with one «Повторить» (RR2.3, RR2.4): under the line of the state
 * — the line of the reading while it goes, the open end «Книги прочитаны — можно спрашивать» once it is done. «Открыть настройки
 * помощника» goes with a cut answer and a refusal; while the stop sum is spent «Изменить сумму…» stands before «Повторить»,
 * which waits with its reason.
 */
function unreadLine(reading: Reading): ReadingLineWords | null {
  const saying = unreadSaying(reading, unreadReasonWords);
  if (!saying) return null;
  const spent = reading.stopAt !== null && reading.spent >= reading.stopAt;
  return {
    label: saying.label, title: saying.title,
    actions: [
      ...(saying.reason === 'cut_answer' || saying.reason === 'refused' ? [{ kind: 'settings' as const, label: readingButtons.settings }] : []),
      ...(spent ? [{ kind: 'sum' as const, label: readingButtons.sum }] : []),
      { kind: 'retryUnread', label: readingButtons.retry, ...(spent ? { disabledReason: repeatWords.spent } : {}) },
    ],
    ask: false, tone: 'stalled', progress: null, under: true,
  };
}

/**
 * The line of the reading of the books by «Свой ИИ» by the status of the server (specs/2026-11-13-desk-consent-web.md RW1.1):
 * the helper not connected; ready to read — «Прочитать книги…»; going — the chapters, the time, what was spent of the sum,
 * that a question may be asked, «Изменить сумму…» and «Пауза»; a pause with its reason and its way out; the provider changed;
 * the chapters read while the names and the threads wait; the chapters that did not read with «Повторить». Every word and
 * number is of the status (`reading`, `preparation`); the page counts nothing. Null — nothing of the reading by «Свой ИИ»: the
 * open words of the line stand. The helper not connected is said by the one reason of the edition — the link with the account
 * broken or waiting (specs/2026-11-15-desk-account-link-web.md AW3.4) — and, until it is read, by the open words.
 */
export function readingLinesOf(
  status: IndexingStatus | undefined, helper: HelperReason | null = null, unconnected: HelperReason | null = null,
): ReadingLineWords[] | null {
  if (!status) return null;
  const preparation = status.preparation;
  // The status says nothing of a waiting while the answers of the settings and the link say the helper is not connected — a read
  // saga (decision of the coordinator 15.11): the same reason with its button. A saga with no text yet has no line of the reading.
  const silent = unconnected && status.published.exists && !preparation?.waiting && !status.reading ? unconnected : null;
  if (preparation?.waiting === 'helper' || silent) {
    const reason = silent ?? helper;
    const action = reason?.action ?? 'connect';
    return [{
      label: reason?.label ?? 'Помощник ещё не подключён', title: reason?.title ?? waitingLine(preparation)!,
      actions: [{ kind: action, label: readingButtons[action] }], ask: false, tone: 'work', progress: null, under: false,
    }];
  }
  const reading = status.reading;
  if (!reading) return null;
  const read = chaptersOf(reading.chaptersRead, reading.chaptersTotal);
  const names = reading.namesWait || reading.threadsWait;
  const failed = unreadLine(reading);
  switch (reading.state) {
    case 'waiting_consent': {
      const left = Math.max(reading.chaptersTotal - reading.chaptersRead, 0);
      const ready = names ? restLeft(reading) : `Помощник готов прочитать книги: ${formatCount(left, 'главу', 'главы', 'глав')}`;
      return [{
        label: 'Помощник готов прочитать книги',
        title: reading.reason === 'provider_changed' ? `Провайдер помощника сменился. ${ready}` : ready,
        actions: [{ kind: 'consent', label: readingButtons.consent }], ask: false, tone: 'work', progress: null, under: false,
      }];
    }
    case 'reading': {
      const time = !names && reading.remainingSeconds !== null ? [`осталось ${aboutTimeWords(reading.remainingSeconds)}`] : [];
      const going: ReadingLineWords = {
        label: 'Помощник читает книги',
        title: [names ? restGoing(reading) : `Помощник читает книги: ${read}`, ...time, `потрачено ${spentWords(reading)}`, askWords(preparation?.askable ?? true)].join(' · '),
        // «Изменить сумму…» while it goes too: the author who sees the sum near changes it without stopping the reading.
        actions: [{ kind: 'sum', label: readingButtons.sum }, { kind: 'pause', label: readingButtons.pause }],
        ask: true, tone: 'work',
        progress: !names && reading.chaptersTotal > 0 ? reading.chaptersRead / reading.chaptersTotal : null,
        under: false,
      };
      return failed ? [going, failed] : [going];
    }
    case 'paused': {
      // A pause of the helper of the account is not this block's: its block says it.
      if (!reading.pause || !Object.hasOwn(pauseWords, reading.pause)) return null;
      const reason = reading.pause as OwnPause;
      const title = reason === 'author'
        ? (names ? `${pauseWords.author(reading)}. ${restLeft(reading)}` : `${pauseWords.author(reading)}: прочитано ${read}`)
        : `${pauseWords[reason](reading)}. ${names ? restLeft(reading) : `Прочитано ${read}`}`;
      return [{ label: 'Чтение на паузе', title, actions: pauseActions(reading), ask: false, tone: 'stalled', progress: null, under: false }];
    }
    case 'done':
      // The end is the open line's — «Книги прочитаны — можно спрашивать» with its question; what did not read or come together
      // stands under it.
      return failed ? [failed] : null;
    default:
      return null;
  }
}
