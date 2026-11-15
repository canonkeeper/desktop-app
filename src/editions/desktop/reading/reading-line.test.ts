// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { hiddenIn } from '../../../shared/copy/dictionary';
import { helperReasonOf } from '../account/helper-reason';
import type { ReadingEstimate } from './reading-api';
import { readingLinesOf, type Reading } from './reading-line';
import { consentMessage, consentWords, noSumMessage } from './reading-words';

const plain = (text: string) => text.replace(/\u00a0/gu, ' ');

const going: Reading = {
  state: 'reading', source: 'own', reason: null, pause: null, chaptersRead: 212, chaptersTotal: 612, namesWait: false, threadsWait: false, unread: null, unreadRest: null,
  spent: 240_000_000, approximate: true, stopAt: 1_000_000_000, step: 1_000_000_000, unit: 'RUB', remainingSeconds: 10_800,
};

function statusOf(reading: Partial<Reading> | null, waiting?: 'helper' | 'consent' | 'paused', askable = true): IndexingStatus {
  return {
    published: { exists: true, context: { state: 'processing', currentBook: null, retryableFailedBook: null } }, steps: [],
    preparation: {
      remainingSeconds: null, letterPromised: false, chaptersRead: 212, chaptersTotal: 612, booksRead: 4, booksTotal: 13,
      currentBookId: 'book-05', askable, books: [], ...(waiting ? { waiting } : {}),
    },
    ...(reading ? { reading: { ...going, ...reading } } : {}),
  } as IndexingStatus;
}

/** The words of a line: its title and its buttons, as the author reads them. */
function said(status: IndexingStatus) {
  return readingLinesOf(status)?.map((line) => ({ title: plain(line.title), buttons: line.actions.map((action) => plain(action.label)), ask: line.ask, tone: line.tone }));
}

// Decision of the coordinator 15.11 (specs/2026-11-15-desk-account-words-web.md): the helper not connected by the answers of the
// settings and the link says its reason while the status says nothing of a waiting — a read saga; the server's waiting, its
// reading and its consent go first.
describe('the helper not connected while the status is silent', () => {
  const broken = helperReasonOf('account', 'broken');
  const readSaga = { published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } }, steps: [] } as unknown as IndexingStatus;

  it('a read saga: the reason with its button', () => {
    expect(readingLinesOf(readSaga, null, broken)?.map((line) => [line.label, line.title, line.actions.map((action) => action.label)])).toEqual([
      ['Помощник не подключён', 'Помощник не подключён: связь с аккаунтом прервана', ['Связать снова']],
    ]);
  });

  it('the server says a reading or a waiting: its line, not the reason of the page', () => {
    expect(readingLinesOf(statusOf({}), null, broken)?.[0]?.label).toBe('Помощник читает книги');
    expect(readingLinesOf(statusOf({ state: 'waiting_consent' }, 'consent'), null, broken)?.[0]?.label).toBe('Помощник готов прочитать книги');
    expect(readingLinesOf(readSaga, null, null)).toBeNull();
  });

  it('a saga with no text yet: no line', () => {
    const empty = { published: { exists: false, context: null }, steps: [] } as unknown as IndexingStatus;
    expect(readingLinesOf(empty, null, broken)).toBeNull();
  });
});

// specs/2026-11-13-desk-consent-web.md RW1.1: each state of the reading by the object of the server — its words and its
// buttons (board 44·28 and the table Data of specs/2026-11-13-desk-consent.md).
describe('the line of the reading by «Свой ИИ»', () => {
  it('waits for the helper: its one line with «Подключить помощника»', () => {
    expect(said(statusOf(null, 'helper'))).toEqual([{
      title: 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас', buttons: ['Подключить помощника'], ask: false, tone: 'work',
    }]);
  });

  it('waits for the consent: the chapters left and «Прочитать книги…»', () => {
    expect(said(statusOf({ state: 'waiting_consent', spent: 0, stopAt: null, step: null, remainingSeconds: null }, 'consent'))).toEqual([{
      title: 'Помощник готов прочитать книги: 400 глав', buttons: ['Прочитать книги…'], ask: false, tone: 'work',
    }]);
  });

  it('says the provider changed: the consent no longer acts', () => {
    expect(said(statusOf({ state: 'waiting_consent', reason: 'provider_changed' }, 'consent'))?.[0]?.title)
      .toBe('Провайдер помощника сменился. Помощник готов прочитать книги: 400 глав');
  });

  it('goes: the chapters, the time, what was spent of the sum, the question — «Изменить сумму…», «Пауза» and the place asks', () => {
    expect(said(statusOf({}))).toEqual([{
      title: 'Помощник читает книги: 212 из 612 глав · осталось около 3 часов · потрачено ≈ 240 из 1 000 ₽ · спросить можно уже сейчас',
      buttons: ['Изменить сумму…', 'Пауза'], ask: true, tone: 'work',
    }]);
    expect(readingLinesOf(statusOf({}))?.[0]?.progress).toBeCloseTo(212 / 612);
  });

  it.each([
    ['in tokens without prices', { unit: 'tokens', spent: 4_000_000, stopAt: 25_000_000 }, 'потрачено ≈ 4 из 25 млн токенов'],
    ['without a sum, only what was spent', { stopAt: null, step: null }, 'потрачено ≈ 240 ₽'],
    ['counted exactly — no «≈»', { approximate: false }, 'потрачено 240 из 1 000 ₽'],
    ['in dollars', { unit: 'USD', spent: 4_100_000, stopAt: 10_000_000 }, 'потрачено ≈ $4,10 из $10'],
  ] as const)('counts %s', (_case, reading, words) => {
    expect(said(statusOf(reading))?.[0]?.title).toContain(words);
  });

  it('says the time only when the server has one, and that a question waits before the search finds a chapter', () => {
    const title = said(statusOf({ remainingSeconds: null }, undefined, false))?.[0]?.title;
    expect(title).toBe('Помощник читает книги: 212 из 612 глав · потрачено ≈ 240 из 1 000 ₽ · спросить можно будет через пару минут');
  });

  it('agrees the chapters: 1 главы, 2 глав', () => {
    expect(said(statusOf({ chaptersRead: 0, chaptersTotal: 1, remainingSeconds: null }))?.[0]?.title).toMatch(/^Помощник читает книги: 0 из 1 главы · /u);
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 0, chaptersTotal: 1 }, 'consent'))?.[0]?.title).toBe('Помощник готов прочитать книги: 1 главу');
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 0, chaptersTotal: 2 }, 'consent'))?.[0]?.title).toBe('Помощник готов прочитать книги: 2 главы');
  });

  it('goes with the chapters read: the names and the threads, without a count of chapters', () => {
    expect(said(statusOf({ chaptersRead: 612, namesWait: true, threadsWait: true, spent: 690_000_000 }))?.[0]?.title)
      .toBe('Главы прочитаны — помощник сводит имена героев и сюжетные нити · потрачено ≈ 690 из 1 000 ₽ · спросить можно уже сейчас');
    expect(said(statusOf({ state: 'waiting_consent', chaptersRead: 612, namesWait: true, threadsWait: true }, 'consent'))?.[0]?.title)
      .toBe('Главы прочитаны — помощнику осталось свести имена героев и сюжетные нити');
  });

  // specs/2026-11-15-desk-reading-rest.md RR2.7: what is brought together — by the two signs of the server, the same words in the
  // line of the reading, its pause and its wait for the consent (the windows — reading-words.test).
  it.each([
    ['the names', { namesWait: true }, 'имена героев'],
    ['the threads', { threadsWait: true }, 'сюжетные нити'],
    ['both', { namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити'],
  ] as const)('says what is brought together: %s', (_case, signs, words) => {
    const read = { chaptersRead: 612, ...signs };
    expect(said(statusOf(read))?.[0]?.title).toMatch(new RegExp(`^Главы прочитаны — помощник сводит ${words} · `, 'u'));
    expect(said(statusOf({ ...read, state: 'waiting_consent' }, 'consent'))?.[0]?.title).toBe(`Главы прочитаны — помощнику осталось свести ${words}`);
    expect(said(statusOf({ ...read, state: 'paused', pause: 'author' }, 'paused'))?.[0]?.title)
      .toBe(`Чтение на паузе по вашей просьбе. Главы прочитаны — помощнику осталось свести ${words}`);
  });

  it.each([
    ['author', {}, 'Чтение на паузе по вашей просьбе: прочитано 212 из 612 глав', ['Продолжить']],
    ['sum', { spent: 1_000_000_000, approximate: false }, 'Чтение на паузе: потрачено 1 000 ₽ — сумма, на которой вы просили остановиться. Прочитано 212 из 612 глав', ['Изменить сумму…', 'Продолжить ещё на 1 000 ₽']],
    ['sum', { spent: 980_000_000 }, 'Чтение на паузе: следующий запрос превысил бы сумму, на которой вы просили остановиться. Потрачено ≈ 980 из 1 000 ₽. Прочитано 212 из 612 глав', ['Изменить сумму…', 'Продолжить ещё на 1 000 ₽']],
    ['prices', {}, 'Чтение на паузе: цены или валюта в настройках изменились. Назовите сумму остановки заново. Прочитано 212 из 612 глав', ['Изменить сумму…']],
    ['funds', {}, 'Чтение на паузе: провайдер отказал — кончились средства на счёте или он пока не принимает запросы. Прочитано 212 из 612 глав', ['Продолжить']],
    ['key', {}, 'Чтение на паузе: провайдер не принял ключ. Прочитано 212 из 612 глав', ['Открыть настройки помощника']],
    ['model', {}, 'Чтение на паузе: у провайдера нет модели с таким названием. Прочитано 212 из 612 глав', ['Открыть настройки помощника']],
    ['connection', {}, 'Чтение на паузе: нет связи с провайдером. Прочитано 212 из 612 глав', ['Повторить']],
    ['no_answer', {}, 'Чтение на паузе: провайдер не отвечает. Прочитано 212 из 612 глав', ['Повторить']],
  ] as const)('stands at the pause «%s» with its words and its way out', (pause, reading, title, buttons) => {
    expect(said(statusOf({ state: 'paused', pause, ...reading }, 'paused'))).toEqual([{ title, buttons: [...buttons], ask: false, tone: 'stalled' }]);
  });

  // specs/2026-11-15-desk-reading-no-answer.md NA1.3 (Data): the provider does not answer — what was read, or what is left to
  // bring together once the chapters are read, and «Повторить», which goes on with the same operation of the consent.
  it.each([
    [{ namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити'],
    [{ namesWait: true }, 'имена героев'],
    [{ threadsWait: true }, 'сюжетные нити'],
  ] as const)('stands at «провайдер не отвечает» with the chapters read: %#', (signs, words) => {
    const lines = readingLinesOf(statusOf({ state: 'paused', pause: 'no_answer', chaptersRead: 612, ...signs }, 'paused'));
    expect(lines?.map((line) => plain(line.title))).toEqual([`Чтение на паузе: провайдер не отвечает. Главы прочитаны — помощнику осталось свести ${words}`]);
    expect(lines?.[0]?.actions).toEqual([{ kind: 'continue', label: 'Повторить' }]);
  });

  // The words of the pauses are one table of the reasons of «Свой ИИ»: every reason the contract names but those of the helper
  // of the account (their words come with the pages of the account) has its own sentence, never the line of a pause without one.
  it('says every pause of «Свой ИИ» the contract names in its own words', () => {
    const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      components: { schemas: { ReadingPause: { enum: Reading['pause'][] } } };
    };
    const account = ['characters', 'reading_fence', 'unfit', 'unavailable'];
    const own = contract.components.schemas.ReadingPause.enum.filter((pause) => !account.includes(pause!));
    expect(own).toContain('no_answer');
    for (const pause of own) {
      const title = said(statusOf({ state: 'paused', pause, spent: 1_000_000_000 }, 'paused'))?.[0]?.title;
      expect(title, pause!).toMatch(/^Чтение на паузе( по вашей просьбе)?[:.] /u);
      expect(title, pause!).not.toBe('Чтение на паузе: прочитано 212 из 612 глав');
    }
  });

  // NA2.2: the chapters the provider did not take — the way to the settings of the helper, another model, and «Повторить».
  it('says the chapters the provider did not take with the way to another model', () => {
    const lines = readingLinesOf(statusOf({ unread: { chapters: 3, reason: 'refused' } }));
    expect(lines?.[1] && [plain(lines[1].label), plain(lines[1].title), lines[1].actions.map((action) => action.label)]).toEqual([
      '3 главы не прочитались', '3 главы не прочитались: провайдер не принял запрос — попробуйте другую модель для чтения книг',
      ['Открыть настройки помощника', 'Повторить'],
    ]);
  });

  it('names X of «Продолжить ещё на X» by the server, in tokens too', () => {
    const line = said(statusOf({ state: 'paused', pause: 'sum', unit: 'tokens', spent: 25_000_000, stopAt: 25_000_000, step: 25_000_000, approximate: false }, 'paused'));
    expect(line?.[0]?.title).toBe('Чтение на паузе: потрачено 25 млн токенов — сумма, на которой вы просили остановиться. Прочитано 212 из 612 глав');
    expect(line?.[0]?.buttons).toEqual(['Изменить сумму…', 'Продолжить ещё на 25 млн токенов']);
  });

  // After «Продолжить ещё на 3 ₽» at 2,60 ₽ the server stops at 5,60 ₽ and keeps the step 3 ₽: the next pause offers the step,
  // never the sum (CN4.1: `R.step`).
  it('names X by the step of the server, which is not the sum once the reading went on by a step', () => {
    const line = said(statusOf({ state: 'paused', pause: 'sum', spent: 5_200_000, stopAt: 5_600_000, step: 3_000_000, approximate: false }, 'paused'));
    expect(line?.[0]?.title).toContain('Потрачено 5,20 из 5,60 ₽');
    expect(line?.[0]?.buttons).toEqual(['Изменить сумму…', 'Продолжить ещё на 3 ₽']);
  });

  it('pauses the author with the chapters read: the names and the threads left', () => {
    expect(said(statusOf({ state: 'paused', pause: 'author', chaptersRead: 612, namesWait: true, threadsWait: true }, 'paused'))?.[0]?.title)
      .toBe('Чтение на паузе по вашей просьбе. Главы прочитаны — помощнику осталось свести имена героев и сюжетные нити');
  });

  // Board 44·28 «Не прочитались»: the open line of the end stands — «Книги прочитаны — можно спрашивать» with «Задать первый
  // вопрос» — and under it one line of all that did not read or come together, with one «Повторить»
  // (specs/2026-11-15-desk-reading-rest.md RR2.3).
  it('is done but for the chapters that did not read: a line under the open line of the end, with «Повторить»', () => {
    const status = statusOf({ state: 'done', remainingSeconds: null, unread: { chapters: 3, reason: 'unreadable' } });
    expect(said(status)).toEqual([{
      title: '3 главы не прочитались: ответ помощника не удалось разобрать', buttons: ['Повторить'], ask: false, tone: 'stalled',
    }]);
    expect(readingLinesOf(status)?.map((line) => [line.under, line.label])).toEqual([[true, '3 главы не прочитались']]);
    expect(said(statusOf({ state: 'done', unread: { chapters: 1, reason: 'unreadable' } }))?.[0]?.title).toBe('1 глава не прочиталась: ответ помощника не удалось разобрать');
    expect(said(statusOf({ state: 'done', unread: { chapters: 11, reason: 'cut_answer' } }))?.[0]?.title)
      .toBe('11 глав не прочитались: ответ помощника оборвался — увеличьте «Предел ответа»');
  });

  type Rest = NonNullable<Reading['unreadRest']>;
  const rest = (names: Rest['names'], threads: Rest['threads'] = null): Partial<Reading> => ({ state: 'done', remainingSeconds: null, unreadRest: { names, threads } });

  // RR2.3, Data: «<что>: <причина>. <что это значит>» for each of the seven sets of what did not read or come together.
  it.each([
    ['the names', rest('unreadable'), 'Имена героев не сведены', 'Имена героев не сведены: ответ помощника не удалось разобрать. Спрашивать можно — героя под другим именем помощник может не узнать'],
    ['the threads', rest(null, 'unreadable'), 'Сюжетные нити не сведены', 'Сюжетные нити не сведены: ответ помощника не удалось разобрать. Спрашивать можно — о незакрытых нитях помощник ответит хуже'],
    ['both', rest('unreadable', 'unreadable'), 'Имена героев и сюжетные нити не сведены', 'Имена героев и сюжетные нити не сведены: ответ помощника не удалось разобрать. Спрашивать можно — ответы будут менее точными'],
    ['the chapters and the names', { ...rest('unreadable'), unread: { chapters: 2, reason: 'unreadable' } }, '2 главы не прочитались, имена героев не сведены', '2 главы не прочитались, имена героев не сведены: ответ помощника не удалось разобрать. Спрашивать можно — помощник ответит по прочитанному'],
    ['the chapters and the threads', { ...rest(null, 'unreadable'), unread: { chapters: 5, reason: 'unreadable' } }, '5 глав не прочитались, сюжетные нити не сведены', '5 глав не прочитались, сюжетные нити не сведены: ответ помощника не удалось разобрать. Спрашивать можно — помощник ответит по прочитанному'],
    ['the chapters and both', { ...rest('unreadable', 'unreadable'), unread: { chapters: 1, reason: 'unreadable' } }, '1 глава не прочиталась, имена героев и сюжетные нити не сведены', '1 глава не прочиталась, имена героев и сюжетные нити не сведены: ответ помощника не удалось разобрать. Спрашивать можно — помощник ответит по прочитанному'],
  ] as const)('says what did not come together: %s', (_case, reading, label, title) => {
    const lines = readingLinesOf(statusOf(reading));
    expect(lines?.map((line) => [plain(line.label), plain(line.title), line.under, line.tone, line.ask])).toEqual([[label, title, true, 'stalled', false]]);
    expect(lines?.[0]?.actions.filter((action) => action.label === 'Повторить')).toHaveLength(1);
  });

  // Data: the reasons in the words of «Свой ИИ» — a cut answer and a refusal with the way to the settings of the helper — and
  // the one reason of the line, the first of what is marked in the order cut_answer, refused, no_answer, unreadable.
  it.each([
    ['cut_answer', 'ответ помощника оборвался — увеличьте «Предел ответа»', ['Открыть настройки помощника', 'Повторить']],
    ['refused', 'провайдер не принял запрос — попробуйте другую модель для чтения книг', ['Открыть настройки помощника', 'Повторить']],
    ['no_answer', 'провайдер не ответил', ['Повторить']],
    ['unreadable', 'ответ помощника не удалось разобрать', ['Повторить']],
  ] as const)('says the reason %s with its buttons', (reason, words, buttons) => {
    const line = said(statusOf(rest(reason)))?.[0];
    expect(line?.title).toBe(`Имена героев не сведены: ${words}. Спрашивать можно — героя под другим именем помощник может не узнать`);
    expect(line?.buttons).toEqual([...buttons]);
  });

  it.each([
    [{ ...rest('unreadable', 'no_answer'), unread: { chapters: 1, reason: 'cut_answer' } }, 'ответ помощника оборвался'],
    [rest('unreadable', 'refused'), 'провайдер не принял запрос'],
    [rest('unreadable', 'no_answer'), 'провайдер не ответил'],
  ] as const)('takes one reason by the order of Data: %#', (reading, words) => {
    expect(said(statusOf(reading))?.[0]?.title).toContain(`: ${words}`);
  });

  // Data: every two reasons, whichever of the names and the threads carries which, give the one that comes first in the order
  // cut_answer, refused, no_answer, unreadable — and the button of the settings goes with it.
  const dataOrder = ['cut_answer', 'refused', 'no_answer', 'unreadable'] as const;
  const ownWords = {
    cut_answer: 'ответ помощника оборвался', refused: 'провайдер не принял запрос', no_answer: 'провайдер не ответил', unreadable: 'ответ помощника не удалось разобрать',
  } as const;
  const pairs = dataOrder.flatMap((first, at) => dataOrder.slice(at + 1).flatMap((second) => [[first, second, first], [second, first, first]] as const));
  it.each(pairs)('takes of the names %s and of the threads %s the reason %s', (names, threads, first) => {
    const line = said(statusOf(rest(names, threads)))?.[0];
    expect(line?.title).toContain(`не сведены: ${ownWords[first]}`);
    expect(line?.buttons).toEqual(first === 'cut_answer' || first === 'refused' ? ['Открыть настройки помощника', 'Повторить'] : ['Повторить']);
  });

  // RR2.4: while the stop sum is spent «Изменить сумму…» stands before «Повторить», which waits and says why.
  it('waits with «Повторить» while the stop sum is spent', () => {
    const lines = readingLinesOf(statusOf({ ...rest('unreadable'), spent: 1_000_000_000, stopAt: 1_000_000_000 }));
    expect(lines?.[0]?.actions.map((action) => action.label)).toEqual(['Изменить сумму…', 'Повторить']);
    expect(lines?.[0]?.actions[1]).toMatchObject({ kind: 'retryUnread', disabledReason: 'Сумма остановки уже потрачена — назовите новую в «Изменить сумму…»' });
    expect(readingLinesOf(statusOf(rest('unreadable')))?.[0]?.actions[0]).not.toHaveProperty('disabledReason');
  });

  // RR2.3: no line of failure stands at a pause or while the reading waits for the consent: the line of the pause speaks.
  it('stands neither at a pause nor while the consent is waited for', () => {
    expect(said(statusOf({ ...rest('unreadable'), state: 'paused', pause: 'author' }, 'paused'))?.map((line) => line.title))
      .toEqual(['Чтение на паузе по вашей просьбе: прочитано 212 из 612 глав']);
    expect(said(statusOf({ ...rest('unreadable'), state: 'waiting_consent' }, 'consent'))).toHaveLength(1);
  });

  it('goes with what did not come together: a second line under the line of the reading', () => {
    const lines = said(statusOf({ chaptersRead: 612, threadsWait: true, unreadRest: { names: 'unreadable', threads: null } }));
    expect(lines?.map((line) => line.title)).toEqual([
      expect.stringMatching(/^Главы прочитаны — помощник сводит сюжетные нити · /u),
      'Имена героев не сведены: ответ помощника не удалось разобрать. Спрашивать можно — героя под другим именем помощник может не узнать',
    ]);
    expect(lines?.[1]?.buttons).toEqual(['Повторить']);
  });

  it('goes with chapters that did not read: a second line with «Повторить»', () => {
    const lines = said(statusOf({ unread: { chapters: 2, reason: 'unreadable' } }));
    expect(lines?.map((line) => line.title)).toEqual([
      expect.stringMatching(/^Помощник читает книги: 212 из 612 глав/u), '2 главы не прочитались: ответ помощника не удалось разобрать',
    ]);
    expect(lines?.[1]?.buttons).toEqual(['Повторить']);
    expect(readingLinesOf(statusOf({ unread: { chapters: 2, reason: 'unreadable' } }))?.map((line) => line.under)).toEqual([false, true]);
  });

  // A value of the server changes the words: the page counts nothing of its own.
  it('follows the values of the server', () => {
    expect(said(statusOf(rest('unreadable')))?.[0]?.title).not.toBe(said(statusOf(rest('no_answer')))?.[0]?.title);
    expect(said(statusOf({ ...rest('unreadable'), unread: { chapters: 4, reason: 'unreadable' } }))?.[0]?.title).toMatch(/^4 главы /u);
  });

  // RR2.7: the window of the consent and the window without a sum say what is left by the same two signs of the server.
  it.each([
    [{ namesWait: true, threadsWait: false }, 'имена героев'],
    [{ namesWait: false, threadsWait: true }, 'сюжетные нити'],
    [{ namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити'],
  ])('says in the windows what is left: %o', (signs, words) => {
    const estimate = { chapters: 0, host: 'api.deepseek.com', local: false, amount: 4_000_000, unit: 'RUB', inputTokens: 1, outputTokens: 1, ...signs } as ReadingEstimate;
    expect(plain(consentMessage(estimate))).toBe(`Главы прочитаны. Помощнику осталось свести ${words}. Текст книг уйдёт вашему провайдеру api.deepseek.com`);
    expect(plain(noSumMessage(estimate))).toMatch(new RegExp(`^Помощник сведёт ${words} и прочитает книги, которые вы добавите позже`, 'u'));
  });

  // The words of the block pass the dictionary of the page in its place (specs/2026-11-13-desk-consent-web.md RW4.1).
  it('says nothing the dictionary hides', () => {
    const sets: Partial<Reading>[] = [
      rest('cut_answer', 'refused'), rest('no_answer'), rest(null, 'unreadable'),
      { ...rest('unreadable'), unread: { chapters: 3, reason: 'cut_answer' }, spent: 1_000_000_000, stopAt: 1_000_000_000 },
      { chaptersRead: 612, namesWait: true }, { chaptersRead: 612, threadsWait: true },
    ];
    for (const reading of sets) {
      for (const line of readingLinesOf(statusOf(reading)) ?? []) {
        const sayings = [line.label, line.title, ...line.actions.map((action) => action.label), ...line.actions.map((action) => ('disabledReason' in action ? action.disabledReason ?? '' : ''))];
        for (const saying of sayings) expect(hiddenIn(saying, 'src/editions/desktop/reading/reading-line.ts').map((word) => word.says), saying).toEqual([]);
      }
    }
    for (const [names, threads] of [[true, true], [true, false], [false, true]] as const) {
      const saying = consentWords.namesOnly(names, threads);
      expect(hiddenIn(saying, 'src/editions/desktop/reading/reading-words.ts').map((word) => word.says), saying).toEqual([]);
    }
  });

  it('says nothing of its own when the server says nothing of the reading by «Свой ИИ»: the open line stands', () => {
    expect(readingLinesOf(statusOf(null))).toBeNull();
    expect(readingLinesOf(undefined)).toBeNull();
  });
});
