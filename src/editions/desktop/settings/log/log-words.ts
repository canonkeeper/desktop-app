// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../../shared/api';
import { formatCount, pluralize } from '../../../../ui/copy';
import { amountWords, approximately, type Currency, tokensWords } from '../../reading/amounts';

export type SendLogKind = Schema<'SendLogKind'>;

/**
 * The words of the part «Журнал» (specs/2026-11-14-desk-journal.md Data): the kinds of the rows and of the choice, the lines
 * of the part and its states, the question of the clearing and its outcomes. What went, to whom and how a sending ended are
 * the server's words.
 */
export const logWords = {
  about: 'Что из ваших книг и чатов приложение отправляло помощнику и отдавало вашим программам на этом компьютере: когда, кому и что.',
  places: 'Места названы так, как они сейчас стоят в книгах.',
  kinds: {
    all: 'Все виды', question: 'Вопрос', reading: 'Чтение книги', materials: 'Ваши материалы', check: 'Проверка связи', program: 'Свой Claude или Codex',
  } satisfies Record<SendLogKind | 'all', string>,
  /** Under a row of the readings of a program (specs/2026-11-15-desk-mcp.md Data «журнал»). */
  given: 'Отдано вашей программе — куда она отправит это дальше, решают она и её провайдер',
  choice: 'Вид отправок',
  running: 'идёт',
  firstTime: 'весь прежний разговор этого чата — впервые этому получателю',
  chatGone: 'удалённый чат',
  more: 'Подробно',
  less: 'Свернуть',
  next: 'Показать ещё',
  save: 'Сохранить в файл…',
  clear: 'Очистить журнал…',
  conversation: 'Вместе с вопросом уходят указания помощнику и прежний разговор этого чата, если он был',
  repeat: 'повтор',
  empty: { title: 'Ещё ничего не уходило', why: 'Здесь появится каждая отправка помощнику и каждое чтение вашей программой: время, куда, что ушло и сколько.' },
  clearedEmpty: 'Новые отправки появятся здесь.',
  noKind: { title: 'Отправок этого вида нет', reset: 'Показать все виды' },
  question: { title: 'Очистить журнал?', also: 'Книги и чаты не изменятся.', action: 'Очистить журнал' },
  cleared: 'Журнал очищен',
  notCleared: 'Журнал не очистился. Попробуйте ещё раз',
  /** The file goes to «Загрузки» of the window, as the file of a saga says (src/editions/desktop/saga-file/words.ts). */
  saved: (fileName: string) => `Журнал сохранён в файл: Загрузки → ${fileName}`,
  notSaved: 'Журнал не сохранился. Попробуйте ещё раз',
  failed: 'Не открылся журнал',
} as const;

const dayMonth = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });
const dayMonthYear = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
const clock = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });

/** The day of a moment on the computer of the author: «2026-11-13». */
export function dayOf(at: string | Date): string {
  const moment = typeof at === 'string' ? new Date(at) : at;
  return `${moment.getFullYear()}-${String(moment.getMonth() + 1).padStart(2, '0')}-${String(moment.getDate()).padStart(2, '0')}`;
}

/** «Сегодня, 13 ноября», «Вчера, 12 ноября», «11 ноября», «28 декабря 2025» (Data, JR3.1). */
export function dayWords(at: string, now = new Date()): string {
  const moment = new Date(at);
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const named = dayMonth.format(moment).replace(/\s/gu, ' ');
  if (dayOf(moment) === dayOf(now)) return `Сегодня, ${named}`;
  if (dayOf(moment) === dayOf(yesterday)) return `Вчера, ${named}`;
  if (moment.getFullYear() !== now.getFullYear()) return dayMonthYear.format(moment).replace(/\s?г\.$/u, '').replace(/\s/gu, ' ');
  return named;
}

/** «12:40». */
export function timeWords(at: string): string {
  return clock.format(new Date(at));
}

/** «Журнал очищен 13 ноября в 14:05» (Data). */
export function clearedWords(at: string): string {
  return `Журнал очищен ${dayMonth.format(new Date(at)).replace(/\s/gu, ' ')} в ${timeWords(at)}`;
}

/** «6 отправок»; the readings of a program — «6 вызовов» (specs/2026-11-15-desk-mcp.md Data «журнал»). */
export function sendingsWords(count: number, kind?: SendLogKind): string {
  if (kind === 'program') return formatCount(count, 'вызов', 'вызова', 'вызовов');
  return formatCount(count, 'отправка', 'отправки', 'отправок');
}

/** «820 токенов», «48 тыс. токенов», «1,2 млн токенов»: a thousand thousands is a million (the table own-words.json). */
export function thousandsOfTokens(tokens: number): string {
  if (tokens < 1_000) return `${tokens} ${pluralize(tokens, 'токен', 'токена', 'токенов')}`;
  const thousands = Math.round(tokens / 1_000);
  return thousands < 1_000 ? `${thousands} тыс. токенов` : tokensWords(tokens);
}

/**
 * The numbers of «Свой ИИ» of a row (JR3.2): «6 запросов · 48 тыс. токенов · ≈ 3 ₽», an estimate with «≈», a sum in every
 * currency, none without the prices — the file of the journal says them by the same table
 * (backend/internal/domain/modeljournal/testdata/own-words.json).
 */
export function ownWords(own: NonNullable<Schema<'SendLogRow'>['own']>): string {
  const parts = [formatCount(own.requests, 'запрос', 'запроса', 'запросов'), approximately(thousandsOfTokens(own.tokens), own.estimated)];
  for (const cost of own.costs) parts.push(approximately(amountWords(cost.micros, cost.currency as Currency), true));
  return parts.join(' · ');
}
