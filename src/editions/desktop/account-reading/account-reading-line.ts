// SPDX-License-Identifier: AGPL-3.0-only
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { askWords, chaptersOf } from '../../../entities/manuscript/preparation-copy';
import { formatCount } from '../../../ui/copy';
import { aboutTimeWords } from '../reading/amounts';
import { unreadSaying, type Reading } from '../reading/reading-line';
import { restWords } from '../reading/reading-words';
import { updateWords } from '../updates/update-words';
import { accountReadingWords, accountUnreadReasonWords } from './account-reading-words';
import { charactersRange } from './characters';

/**
 * What a button of the line of the account does (AW5.3): a window of the consent, one write of the consent, the page of the tiers
 * of the site, the part «Обновления». No sum and no settings of a provider.
 */
export type AccountAction =
  | { kind: 'consent'; label: string }
  | { kind: 'pause'; label: string }
  | { kind: 'continue'; label: string }
  | { kind: 'retryUnread'; label: string }
  | { kind: 'tariffs' }
  | { kind: 'updates'; label: string };

/** One line of the reading through the account in words, before its buttons are made. */
export interface AccountLineWords {
  label: string;
  title: string;
  /** In their order; the main one last. */
  actions: AccountAction[];
  ask: boolean;
  tone: 'work' | 'stalled' | 'done';
  progress: number | null;
  under: boolean;
}

type AccountPause = 'author' | 'characters' | 'reading_fence' | 'unfit' | 'unavailable' | 'connection';

const tariffs: AccountAction = { kind: 'tariffs' };
const retry: AccountAction = { kind: 'continue', label: accountReadingWords.retry };

/** «Главы прочитаны — помощнику осталось свести …» and «… помощник сводит …» — by the two signs of the server (RR2.7). */
const restLeft = (reading: Reading) => `Главы прочитаны — помощнику осталось свести ${restWords(reading.namesWait, reading.threadsWait)}`;
const restGoing = (reading: Reading) => `Главы прочитаны — помощник сводит ${restWords(reading.namesWait, reading.threadsWait)}`;

/**
 * The words of a pause of the account and its ways out (Data): what was read — or what is left to bring together —, whether it
 * goes on by itself by the rule of the server (`DueOfAccount`: the limits once the site shows room, the connection and the
 * helper of the server once in ten minutes, an old version after a new one, the author never), «Повторить» where a repeat may
 * pass now — the author who changed his tier does not wait for the hourly check; none where it would stand at the same pause.
 */
function pauseLine(reading: Reading, pause: AccountPause): { title: string; actions: AccountAction[] } {
  const names = reading.namesWait || reading.threadsWait;
  const read = names ? restLeft(reading) : `Прочитано ${chaptersOf(reading.chaptersRead, reading.chaptersTotal)}`;
  const characters = reading.limits?.characters;
  switch (pause) {
    case 'author':
      return {
        title: names
          ? `Чтение на паузе по вашей просьбе. ${restLeft(reading)}`
          : `Чтение на паузе по вашей просьбе: прочитано ${chaptersOf(reading.chaptersRead, reading.chaptersTotal)}`,
        actions: [{ kind: 'continue', label: accountReadingWords.continue }],
      };
    case 'characters':
      return {
        title: `${characters
          ? `Чтение на паузе: за 30 дней использовано ${charactersRange(characters.used, characters.limit)} знаков — столько знаков текста даёт ваш тариф`
          : 'Чтение на паузе: на тарифе не хватает знаков'}. ${read}. Продолжится само, когда знаки освободятся`,
        actions: [tariffs, retry],
      };
    case 'reading_fence':
      return {
        title: `Чтение на паузе: помощник достиг предела работы за 30 дней на вашем тарифе. ${read}. Продолжится само, когда предел освободится`,
        actions: [tariffs, retry],
      };
    case 'unfit':
      return {
        title: 'Чтение на паузе: эта версия CanonKeeper устарела для сервера. После обновления чтение продолжится само',
        actions: [{ kind: 'updates', label: updateWords.openUpdates }],
      };
    case 'unavailable':
      return { title: 'Чтение на паузе: помощник на сервере сейчас не отвечает. Продолжится само, когда помощник ответит', actions: [retry] };
    case 'connection':
      return { title: 'Чтение на паузе: нет связи с сервером CanonKeeper. Продолжится само, когда связь вернётся', actions: [retry] };
  }
}

const accountPauses: readonly string[] = ['author', 'characters', 'reading_fence', 'unfit', 'unavailable', 'connection'];

/**
 * What did not come together, under the line of the state, in the neutral words of the account with one «Повторить» (AW5.4): the
 * one function of the words of the names and the threads (`unreadSaying`), no button of the settings.
 */
function unreadLine(reading: Reading): AccountLineWords | null {
  const saying = unreadSaying(reading, accountUnreadReasonWords);
  if (!saying) return null;
  return {
    label: saying.label, title: saying.title, actions: [{ kind: 'retryUnread', label: accountReadingWords.retry }],
    ask: false, tone: 'stalled', progress: null, under: true,
  };
}

/**
 * The line of the reading of the books by the helper of the account by the status of the server
 * (specs/2026-11-15-desk-account-words-web.md AW5.3, Data): it waits for the consent — after a change of the helper too —; it
 * goes, with the chapters, the time and the characters of the tier the site told; the chapters read while the names and the
 * threads come together; a pause with its reason and its way out; what did not come together. Null — nothing of the reading
 * through the account: the open words of the line stand.
 */
export function accountReadingLinesOf(status: IndexingStatus | undefined): AccountLineWords[] | null {
  const reading = status?.reading;
  if (!reading || reading.source !== 'account') return null;
  const preparation = status.preparation;
  const names = reading.namesWait || reading.threadsWait;
  const failed = unreadLine(reading);
  switch (reading.state) {
    case 'waiting_consent': {
      const left = Math.max(reading.chaptersTotal - reading.chaptersRead, 0);
      const ready = names ? restLeft(reading) : `Помощник готов прочитать книги: ${formatCount(left, 'главу', 'главы', 'глав')}`;
      return [{
        label: 'Помощник готов прочитать книги',
        title: reading.reason === 'provider_changed' ? `Книги теперь читает помощник CanonKeeper. ${ready}` : ready,
        actions: [{ kind: 'consent', label: accountReadingWords.consent }], ask: false, tone: 'work', progress: null, under: false,
      }];
    }
    case 'reading': {
      const characters = reading.limits?.characters;
      const parts = names ? [restGoing(reading)] : [
        `Помощник читает книги: ${chaptersOf(reading.chaptersRead, reading.chaptersTotal)}`,
        ...(reading.remainingSeconds !== null ? [`осталось ${aboutTimeWords(reading.remainingSeconds)}`] : []),
        ...(characters ? [`на тарифе за 30 дней использовано ${charactersRange(characters.used, characters.limit)} знаков`] : []),
      ];
      const going: AccountLineWords = {
        label: 'Помощник читает книги',
        title: [...parts, askWords(preparation?.askable ?? true)].join(' · '),
        actions: [{ kind: 'pause', label: accountReadingWords.pause }],
        ask: true, tone: 'work',
        progress: !names && reading.chaptersTotal > 0 ? reading.chaptersRead / reading.chaptersTotal : null,
        under: false,
      };
      return failed ? [going, failed] : [going];
    }
    case 'paused': {
      if (!reading.pause || !accountPauses.includes(reading.pause)) return null;
      const { title, actions } = pauseLine(reading, reading.pause as AccountPause);
      return [{ label: 'Чтение на паузе', title, actions, ask: false, tone: 'stalled', progress: null, under: false }];
    }
    case 'done':
      return failed ? [failed] : null;
    default:
      return null;
  }
}
