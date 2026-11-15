// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { hiddenIn, hiddenWords, readsAt } from '../../../shared/copy/dictionary';
import type { Reading } from '../reading/reading-line';
import { accountReadingLinesOf, type AccountAction } from './account-reading-line';

const plain = (text: string) => text.replace(/\u00a0/gu, ' ');

const limits = { characters: { used: 3_200_000, limit: 40_000_000 }, work: { used: 19_000_000, limit: 320_000_000 } };

const going: Reading = {
  state: 'reading', source: 'account', reason: null, pause: null, chaptersRead: 212, chaptersTotal: 600, namesWait: false, threadsWait: false,
  unread: null, unreadRest: null, spent: 0, approximate: false, stopAt: null, step: null, unit: 'tokens', remainingSeconds: 10_800, limits,
};

function statusOf(reading: Partial<Reading>, waiting?: 'consent' | 'paused', askable = true): IndexingStatus {
  return {
    published: { exists: true, context: { state: 'processing', currentBook: null, retryableFailedBook: null } }, steps: [],
    preparation: {
      remainingSeconds: null, letterPromised: false, chaptersRead: 212, chaptersTotal: 600, booksRead: 4, booksTotal: 13,
      currentBookId: 'book-05', askable, books: [], ...(waiting ? { waiting } : {}),
    },
    reading: { ...going, ...reading },
  } as IndexingStatus;
}

const label = (action: AccountAction) => (action.kind === 'tariffs' ? 'Тарифы' : action.label);

function said(status: IndexingStatus) {
  return accountReadingLinesOf(status)?.map((line) => ({ title: plain(line.title), buttons: line.actions.map(label), tone: line.tone, under: line.under }));
}

const paused = (pause: Reading['pause'], reading: Partial<Reading> = {}) => statusOf({ state: 'paused', pause, ...reading }, 'paused');

// specs/2026-11-15-desk-account-words-web.md AW5.3 (Data, board 44·43): each state of the reading through the account in its own
// words with its buttons — no provider, no tokens, no sum.
describe('the line of the reading through the account', () => {
  it('waits for the consent: the chapters left and «Прочитать книги…»; after a change of the helper says it', () => {
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 586 }, 'consent'))).toEqual([
      { title: 'Помощник готов прочитать книги: 14 глав', buttons: ['Прочитать книги…'], tone: 'work', under: false },
    ]);
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 586, reason: 'provider_changed' }, 'consent'))?.[0]?.title)
      .toBe('Книги теперь читает помощник CanonKeeper. Помощник готов прочитать книги: 14 глав');
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 600, namesWait: true, threadsWait: true }, 'consent'))?.[0])
      .toEqual({ title: 'Главы прочитаны — помощнику осталось свести имена героев и сюжетные нити', buttons: ['Прочитать книги…'], tone: 'work', under: false });
  });

  it('goes: the chapters, the time, the characters of the tier the site told, the question and «Пауза»', () => {
    expect(said(statusOf({}))).toEqual([{
      title: 'Помощник читает книги: 212 из 600 глав · осталось около 3 часов · на тарифе за 30 дней использовано 3,2 из 40 млн знаков · спросить можно уже сейчас',
      buttons: ['Пауза'], tone: 'work', under: false,
    }]);
    expect(accountReadingLinesOf(statusOf({}))?.[0]?.ask).toBe(true);
    expect(accountReadingLinesOf(statusOf({}))?.[0]?.progress).toBeCloseTo(212 / 600);
  });

  it('goes without the limits the site has not told: no third part', () => {
    expect(said(statusOf({ limits: undefined }))?.[0]?.title)
      .toBe('Помощник читает книги: 212 из 600 глав · осталось около 3 часов · спросить можно уже сейчас');
  });

  it.each([
    [{ namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити'],
    [{ namesWait: true }, 'имена героев'],
    [{ threadsWait: true }, 'сюжетные нити'],
  ] as const)('goes with the chapters read: what is brought together, without a time — %o', (signs, words) => {
    const status = statusOf({ chaptersRead: 600, remainingSeconds: null, ...signs });
    expect(said(status)?.[0]).toEqual({ title: `Главы прочитаны — помощник сводит ${words} · спросить можно уже сейчас`, buttons: ['Пауза'], tone: 'work', under: false });
    expect(said(paused('author', { chaptersRead: 600, ...signs }))?.[0]?.title).toBe(`Чтение на паузе по вашей просьбе. Главы прочитаны — помощнику осталось свести ${words}`);
  });

  it.each([
    ['author', 'Чтение на паузе по вашей просьбе: прочитано 212 из 600 глав', ['Продолжить']],
    ['characters', 'Чтение на паузе: за 30 дней использовано 3,2 из 40 млн знаков — столько знаков текста даёт ваш тариф. Прочитано 212 из 600 глав. Продолжится само, когда знаки освободятся', ['Тарифы', 'Повторить']],
    ['reading_fence', 'Чтение на паузе: помощник достиг предела работы за 30 дней на вашем тарифе. Прочитано 212 из 600 глав. Продолжится само, когда предел освободится', ['Тарифы', 'Повторить']],
    ['unfit', 'Чтение на паузе: эта версия CanonKeeper устарела для сервера. После обновления чтение продолжится само', ['Открыть обновления']],
    ['unavailable', 'Чтение на паузе: помощник на сервере сейчас не отвечает. Продолжится само, когда помощник ответит', ['Повторить']],
    ['connection', 'Чтение на паузе: нет связи с сервером CanonKeeper. Продолжится само, когда связь вернётся', ['Повторить']],
  ] as const)('stands at the pause «%s» with its words and its way out', (pause, title, buttons) => {
    expect(said(paused(pause))).toEqual([{ title, buttons: [...buttons], tone: 'stalled', under: false }]);
  });

  it('says the pause of the characters as the server counts them, and without the numbers the site has not told', () => {
    expect(said(paused('characters', { limits: { ...limits, characters: { used: 40_000_000, limit: 40_000_000 } } }))?.[0]?.title)
      .toMatch(/^Чтение на паузе: за 30 дней использовано 40 из 40 млн знаков — столько знаков текста даёт ваш тариф\. /u);
    expect(said(paused('characters', { limits: undefined }))?.[0]?.title)
      .toBe('Чтение на паузе: на тарифе не хватает знаков. Прочитано 212 из 600 глав. Продолжится само, когда знаки освободятся');
    expect(said(paused('reading_fence', { limits: undefined }))?.[0]?.title).not.toMatch(/\d+ из \d+ млн/u);
  });

  it('never offers «Продолжить» where it would stand at the same pause, nor a sum', () => {
    for (const pause of ['characters', 'reading_fence', 'unfit', 'unavailable', 'connection'] as const) {
      expect(said(paused(pause))?.[0]?.buttons, pause).not.toContain('Продолжить');
    }
    expect(said(paused('unfit'))?.[0]?.buttons).toEqual(['Открыть обновления']);
    for (const status of [statusOf({}), paused('characters'), statusOf({ state: 'waiting_consent' }, 'consent')]) {
      expect(said(status)?.flatMap((line) => line.buttons)).not.toContain('Изменить сумму…');
    }
  });

  // AW5.4: what did not come together — the neutral words of the account and one «Повторить», no button of the settings; under
  // the open end, and under the line that goes.
  it.each([
    ['cut_answer', 'ответ помощника оборвался'],
    ['refused', 'запрос не принят'],
    ['no_answer', 'помощник не ответил'],
    ['unreadable', 'ответ помощника не удалось разобрать'],
  ] as const)('says the reason %s of what did not come together in the words of the account', (reason, words) => {
    const lines = said(statusOf({ state: 'done', remainingSeconds: null, unreadRest: { names: reason, threads: null } }));
    expect(lines).toEqual([{
      title: `Имена героев не сведены: ${words}. Спрашивать можно — героя под другим именем помощник может не узнать`, buttons: ['Повторить'], tone: 'stalled', under: true,
    }]);
  });

  it('goes with what did not come together: a second line under the line that goes', () => {
    const lines = said(statusOf({ chaptersRead: 600, threadsWait: true, unreadRest: { names: 'no_answer', threads: null } }));
    expect(lines?.map((line) => line.title)).toEqual([
      expect.stringMatching(/^Главы прочитаны — помощник сводит сюжетные нити · /u),
      'Имена героев не сведены: помощник не ответил. Спрашивать можно — героя под другим именем помощник может не узнать',
    ]);
    expect(lines?.[1]).toMatchObject({ buttons: ['Повторить'], under: true });
  });

  it('follows the values of the server', () => {
    expect(said(statusOf({ limits: { ...limits, characters: { used: 9_900_000, limit: 40_000_000 } } }))?.[0]?.title).toContain('использовано 9,9 из 40 млн знаков');
    expect(said(statusOf({ chaptersRead: 300 }))?.[0]?.title).toContain('300 из 600 глав');
    expect(said(statusOf({}, undefined, false))?.[0]?.title).toMatch(/спросить можно будет через пару минут$/u);
  });

  it('says nothing of a reading of «Свой ИИ» or of none', () => {
    expect(accountReadingLinesOf(statusOf({ source: 'own' }))).toBeNull();
    expect(accountReadingLinesOf(undefined)).toBeNull();
    expect(accountReadingLinesOf(statusOf({ state: 'done' }))).toBeNull();
  });

  // AW5.1: the block lives in its own catalog, out of the part «Помощник» and the block of «Свой ИИ»: the dictionary hides there
  // the words of «Свой ИИ» it allows them.
  it('lives where the dictionary hides «ИИ», «модель», «токены», «ключ»', () => {
    for (const says of ['«ИИ»', '«модель»', '«токены»', '«ключ» в приложении вне части «Свой ИИ»']) {
      const word = hiddenWords.find((each) => each.says === says)!;
      expect(word, says).toBeTruthy();
      expect(readsAt(word, 'src/editions/desktop/account-reading/account-reading-words.ts'), says).toBe(true);
      expect(readsAt(word, 'src/editions/desktop/reading/reading-words.ts'), says).toBe(false);
    }
  });

  // AW5.1: no word of «Свой ИИ» in any state, and nothing the dictionary hides in the catalog of the block.
  it('says no provider, no tokens, no sum, no price in any state, nothing the dictionary hides', () => {
    const states = [
      statusOf({ state: 'waiting_consent' }, 'consent'), statusOf({ state: 'waiting_consent', reason: 'provider_changed' }, 'consent'),
      statusOf({}), statusOf({ limits: undefined }), statusOf({ chaptersRead: 600, namesWait: true }),
      ...(['author', 'characters', 'reading_fence', 'unfit', 'unavailable', 'connection'] as const).flatMap((pause) => [paused(pause), paused(pause, { limits: undefined })]),
      ...(['cut_answer', 'refused', 'no_answer', 'unreadable'] as const).map((reason) => statusOf({ state: 'done', unreadRest: { names: reason, threads: reason } })),
    ];
    for (const status of states) {
      for (const line of accountReadingLinesOf(status) ?? []) {
        for (const saying of [line.label, line.title, ...line.actions.map(label)]) {
          expect(saying).not.toMatch(/провайдер|токен|потрачен|сумм|цен[аыу]|проб[аеу]|ИИ/iu);
          expect(hiddenIn(saying, 'src/editions/desktop/account-reading/account-reading-line.ts').map((word) => word.says), saying).toEqual([]);
        }
      }
    }
  });
});
