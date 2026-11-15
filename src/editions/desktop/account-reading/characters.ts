// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The characters of the tier of the account as the author reads them (specs/2026-11-15-desk-account-words-web.md AW5.6): the
 * one function of the part «Аккаунт», the line of the reading and the window of the consent. What the reading will take is
 * rounded up, what was used down; from a million in millions with a digit after the comma («6,4 млн»), from a hundred million
 * whole, below a million in thousands («480 тыс.»). The page neither subtracts nor adds them: every number is the server's.
 */
export type Rounding = 'up' | 'down';

const tenths = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1, useGrouping: true });

function scaled(value: number, rounding: Rounding): { number: string; unit: string } {
  const round = rounding === 'up' ? Math.ceil : Math.floor;
  if (value < 1_000) return { number: String(round(value)), unit: '' };
  const thousands = round(value / 1_000);
  if (value < 1_000_000 && thousands < 1_000) return { number: String(thousands), unit: 'тыс.' };
  const millions = value >= 100_000_000 ? round(value / 1_000_000) : round(value / 100_000) / 10;
  return { number: tenths.format(millions).replace(/[\s\u202f]/gu, '\u00a0'), unit: 'млн' };
}

/** «6,4 млн», «480 тыс.», «700». */
export function charactersWords(value: number, rounding: Rounding) {
  const { number, unit } = scaled(value, rounding);
  return unit ? `${number} ${unit}` : number;
}

/** «3,2 из 40 млн» — what was used of a limit of the tier, both down; «480 тыс. из 40 млн» when they are not in one unit. */
export function charactersRange(used: number, limit: number) {
  const left = scaled(used, 'down');
  const right = scaled(limit, 'down');
  if (left.unit === right.unit) return right.unit ? `${left.number} из ${right.number} ${right.unit}` : `${left.number} из ${right.number}`;
  return `${charactersWords(used, 'down')} из ${charactersWords(limit, 'down')}`;
}
