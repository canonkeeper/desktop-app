// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import { pluralize } from '../../../ui/copy';

/**
 * The sums and the counts of tokens of the reading of the books by «Свой ИИ» (specs/2026-11-13-desk-consent-web.md RW3.2): the
 * server gives integer millionths of a currency with its ISO 4217 code, or tokens (specs/2026-11-13-desk-consent.md CN1.2);
 * the signs and the rounding are the page's, here alone — the line of the reading, the window of the consent and «Изменить
 * сумму…» read these functions and no other.
 */
export type ReadingUnit = Schema<'ReadingUnit'>;
export type Currency = Exclude<ReadingUnit, 'tokens'>;

/** The space inside a sum: a number never breaks from its sign or from its groups. */
const space = '\u00a0';

/** «₽ — рубль», «$ — доллар», «¥ — юань», «€ — евро» (board 44·25): the dollar before its number, the rest after it. */
export const currencySigns: Record<Currency, { sign: string; name: string; before: boolean }> = {
  RUB: { sign: '₽', name: 'рубль', before: false },
  USD: { sign: '$', name: 'доллар', before: true },
  CNY: { sign: '¥', name: 'юань', before: false },
  EUR: { sign: '€', name: 'евро', before: false },
};

const whole = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 0, useGrouping: true });
const upTo = (digits: number) => new Intl.NumberFormat('ru-RU', { maximumFractionDigits: digits, useGrouping: true });
const oneDigit = upTo(1);
const twoDigits = upTo(2);
const cents = new Intl.NumberFormat('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2, useGrouping: true });

/** Intl of the browser groups by a no-break space or a narrow one: one space for every page. */
function spaced(text: string) {
  return text.replace(/[\s\u202f]/gu, space);
}

/** Money as the page says it: from 100 a whole number («680», «1 000»), below it the cents («4,10»), «,00» dropped. */
function moneyNumber(micros: number): string {
  const value = micros / 1_000_000;
  if (value >= 100) return spaced(whole.format(Math.round(value)));
  if (value > 0 && value < 0.01) return '0,01';
  const text = spaced(cents.format(Math.round(value * 100) / 100));
  return text.endsWith(',00') ? text.slice(0, -3) : text;
}

/**
 * Tokens in millions, as the boards write them: «14», «14,2», «0,05» — from 100 whole, from one a digit after the comma,
 * below one two digits, never «0» for a count that is not nothing.
 */
export function millionsNumber(tokens: number): string {
  const value = tokens / 1_000_000;
  if (value >= 100) return spaced(whole.format(Math.round(value)));
  if (value >= 1) return spaced(oneDigit.format(value));
  if (value > 0 && value < 0.01) return '0,01';
  return spaced(twoDigits.format(value));
}

/** «5 млн»: tokens where the word «токенов» stands once before (the estimate: «≈ 14 млн токенов на вход и ≈ 5 млн на выход»). */
export function millionsWords(tokens: number) {
  return `${millionsNumber(tokens)}${space}млн`;
}

/** «14,2 млн токенов». */
export function tokensWords(tokens: number) {
  return `${millionsWords(tokens)}${space}токенов`;
}

function withSign(number: string, currency: Currency) {
  const { before, sign } = currencySigns[currency];
  return before ? `${sign}${number}` : `${number}${space}${sign}`;
}

/** A sum in its unit: «680 ₽», «$4,10», «14,2 млн токенов». */
export function amountWords(amount: number, unit: ReadingUnit): string {
  return unit === 'tokens' ? tokensWords(amount) : withSign(moneyNumber(amount), unit);
}

/** «≈ 680 ₽» when the sum is an estimate, else the sum. */
export function approximately(words: string, approximate: boolean) {
  return approximate ? `≈${space}${words}` : words;
}

/**
 * What was spent of the stop sum, in one unit (board 44·28): «240 из 1 000 ₽», «$4,10 из $10», «4 из 25 млн токенов»; the sign
 * after the numbers stands once, the dollar before each.
 */
export function rangeWords(spent: number, stop: number, unit: ReadingUnit): string {
  if (unit === 'tokens') return `${millionsNumber(spent)} из ${tokensWords(stop)}`;
  if (currencySigns[unit].before) return `${amountWords(spent, unit)} из ${amountWords(stop, unit)}`;
  return `${moneyNumber(spent)} из ${amountWords(stop, unit)}`;
}

/** What a field of a sum is written in: the sign of the currency, or millions of tokens (boards 44·27, 44·42). */
export function fieldUnitWords(unit: ReadingUnit) {
  return unit === 'tokens' ? `млн${space}токенов` : currencySigns[unit].sign;
}

/**
 * A sum as the field shows it to edit: «1 000», «4,10», «25» (millions of tokens) — no sign, spaces of groups. Money with
 * cents keeps both of them, as the line of the reading writes it («4,80 ₽» there — «4,80» here), and is never rounded to a
 * whole number: the field sends back what it shows.
 */
export function fieldValueOf(amount: number, unit: ReadingUnit): string {
  if (unit === 'tokens') return millionsNumber(amount).replace(/\u00a0/gu, ' ');
  const value = Math.round(amount / 10_000) / 100;
  return spaced((Number.isInteger(value) ? whole : cents).format(value)).replace(/\u00a0/gu, ' ');
}

/**
 * The sum the author wrote in a field, in the unit of the server (integer millionths of the currency, or tokens): «1 000»,
 * «1000,5», «0.5»; empty — no sum (null); what is not a positive number — undefined.
 */
export function fieldAmountOf(text: string, unit: ReadingUnit): number | null | undefined {
  const value = text.replace(/[\s\u00a0\u202f]/gu, '').replace(',', '.');
  if (value === '') return null;
  const digits = unit === 'tokens' ? 3 : 2;
  if (!new RegExp(`^\\d{1,9}(?:\\.\\d{1,${digits}})?$`, 'u').test(value)) return undefined;
  const amount = Math.round(Number(value) * 1_000_000);
  return amount >= 1 ? amount : undefined;
}

/** «около 5 часов», «около 20 минут», «меньше минуты»: a time of the server by its seconds, hours from an hour. */
export function aboutTimeWords(seconds: number): string {
  if (seconds < 60) return 'меньше минуты';
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `около ${minutes}${space}${pluralize(minutes, 'минуты', 'минут', 'минут')}`;
  const hours = Math.max(Math.round(seconds / 3_600), 1);
  return `около ${hours}${space}${pluralize(hours, 'часа', 'часов', 'часов')}`;
}

/** «за 4 минуты», «за 1 час», «меньше чем за минуту»: how long a work took, by its seconds. */
export function tookTimeWords(seconds: number): string {
  if (seconds < 60) return 'меньше чем за минуту';
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `за ${minutes}${space}${pluralize(minutes, 'минуту', 'минуты', 'минут')}`;
  const hours = Math.max(Math.round(seconds / 3_600), 1);
  return `за ${hours}${space}${pluralize(hours, 'час', 'часа', 'часов')}`;
}
