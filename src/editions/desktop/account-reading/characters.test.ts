// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { readingLine } from '../account/account-words';
import { charactersRange, charactersWords } from './characters';

// specs/2026-11-15-desk-account-words-web.md AW5.6: the characters of the tier — one function for the part «Аккаунт», the line
// and the window; «займёт до» up, «использовано» down, below a million in thousands.
describe('the characters of the tier', () => {
  it.each([
    [6_190_000, 'up', '6,2 млн'],
    [6_190_000, 'down', '6,1 млн'],
    [6_400_000, 'up', '6,4 млн'],
    [40_000_000, 'down', '40 млн'],
    [37_960_000, 'down', '37,9 млн'],
    [125_400_000, 'up', '126 млн'],
    [480_400, 'down', '480 тыс.'],
    [480_400, 'up', '481 тыс.'],
    [999_500, 'up', '1 млн'],
    [700, 'up', '700'],
    [0, 'up', '0'],
  ] as const)('%d %s — %s', (value, rounding, words) => {
    expect(charactersWords(value, rounding)).toBe(words);
  });

  it('says what was used of the limit, the unit once when they share it', () => {
    expect(charactersRange(3_200_000, 40_000_000)).toBe('3,2 из 40 млн');
    expect(charactersRange(37_960_000, 40_000_000)).toBe('37,9 из 40 млн');
    expect(charactersRange(480_000, 40_000_000)).toBe('480 тыс. из 40 млн');
    expect(charactersRange(39_999_999, 40_000_000)).toBe('39,9 из 40 млн');
  });

  it('is the number of the part «Аккаунт» too', () => {
    expect(readingLine({ used: 3_260_000, limit: 40_000_000 })).toBe('Чтение книг за 30 дней: использовано 3,2 из 40 млн знаков');
    expect(readingLine({ used: 480_000, limit: 40_000_000 })).toBe('Чтение книг за 30 дней: использовано 480 тыс. из 40 млн знаков');
  });
});
