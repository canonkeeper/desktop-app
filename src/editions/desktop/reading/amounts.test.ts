// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import {
  aboutTimeWords, amountWords, approximately, fieldAmountOf, fieldUnitWords, fieldValueOf, millionsWords, rangeWords, tokensWords,
  tookTimeWords,
} from './amounts';

/** The boards write the spaces as plain ones; the page puts no-break spaces inside a sum and its unit. */
const plain = (text: string) => text.replace(/\u00a0/gu, ' ');

// specs/2026-11-13-desk-consent-web.md RW3.2: one function of the page writes the sums and the counts of tokens by the code of
// the currency and the millionths of the server (examples of the spec and the boards 44·27, 44·28, 44·42).
describe('the sums of the reading', () => {
  it.each([
    [680_000_000, 'RUB', '680 ₽'],
    [1_000_000_000, 'RUB', '1 000 ₽'],
    [4_370_000, 'RUB', '4,37 ₽'],
    [4_000_000, 'RUB', '4 ₽'],
    [4_100_000, 'USD', '$4,10'],
    [10_000_000, 'USD', '$10'],
    [1_234_500_000, 'USD', '$1 235'],
    [52_500_000, 'CNY', '52,50 ¥'],
    [3_000, 'EUR', '0,01 €'],
    [0, 'EUR', '0 €'],
    [14_200_000, 'tokens', '14,2 млн токенов'],
    [25_000_000, 'tokens', '25 млн токенов'],
    [50_000, 'tokens', '0,05 млн токенов'],
    [140_400_000, 'tokens', '140 млн токенов'],
  ] as const)('%d in %s — «%s»', (amount, unit, words) => {
    expect(plain(amountWords(amount, unit))).toBe(words);
  });

  it('never breaks a sum from its groups or its sign', () => {
    expect(amountWords(1_000_000_000, 'RUB')).toBe('1\u00a0000\u00a0₽');
    expect(approximately(tokensWords(4_000_000), true)).toBe('≈\u00a04\u00a0млн\u00a0токенов');
  });

  it('says an estimate with «≈»', () => {
    expect(plain(approximately(amountWords(680_000_000, 'RUB'), true))).toBe('≈ 680 ₽');
    expect(plain(approximately(amountWords(680_000_000, 'RUB'), false))).toBe('680 ₽');
    expect(plain(approximately(tokensWords(14_200_000), true))).toBe('≈ 14,2 млн токенов');
  });

  it('says what was spent of the stop sum in one unit', () => {
    expect(plain(rangeWords(240_000_000, 1_000_000_000, 'RUB'))).toBe('240 из 1 000 ₽');
    expect(plain(rangeWords(4_100_000, 10_000_000, 'USD'))).toBe('$4,10 из $10');
    expect(plain(rangeWords(4_000_000, 25_000_000, 'tokens'))).toBe('4 из 25 млн токенов');
    expect(plain(rangeWords(980_000_000, 1_000_000_000, 'EUR'))).toBe('980 из 1 000 €');
  });

  it('writes the millions of the estimate once with «токенов»', () => {
    expect(plain(`≈ ${tokensWords(14_000_000)} на вход и ≈ ${millionsWords(5_100_000)} на выход`)).toBe('≈ 14 млн токенов на вход и ≈ 5,1 млн на выход');
  });
});

describe('the field of a sum', () => {
  it('is written in the sign of the currency or in millions of tokens', () => {
    expect(fieldUnitWords('RUB')).toBe('₽');
    expect(fieldUnitWords('USD')).toBe('$');
    expect(plain(fieldUnitWords('tokens'))).toBe('млн токенов');
  });

  it('shows a sum to edit without its sign', () => {
    expect(fieldValueOf(1_000_000_000, 'RUB')).toBe('1 000');
    expect(fieldValueOf(4_100_000, 'USD')).toBe('4,10');
    expect(fieldValueOf(4_800_000, 'RUB')).toBe('4,80');
    expect(fieldValueOf(100_500_000, 'RUB')).toBe('100,50');
    expect(fieldValueOf(25_000_000, 'tokens')).toBe('25');
  });

  it.each([
    ['1 000', 'RUB', 1_000_000_000],
    ['1000,5', 'RUB', 1_000_500_000],
    ['4.10', 'USD', 4_100_000],
    ['25', 'tokens', 25_000_000],
    ['0,5', 'tokens', 500_000],
    ['', 'RUB', null],
    ['  ', 'tokens', null],
    ['сто', 'RUB', undefined],
    ['-5', 'RUB', undefined],
    ['0', 'RUB', undefined],
    ['1,234', 'EUR', undefined],
  ] as const)('reads «%s» in %s as %s', (text, unit, amount) => {
    expect(fieldAmountOf(text, unit)).toBe(amount);
  });
});

describe('the times of the reading', () => {
  it.each([
    [30, 'меньше минуты', 'меньше чем за минуту'],
    [61, 'около 2 минут', 'за 2 минуты'],
    [240, 'около 4 минут', 'за 4 минуты'],
    [1_200, 'около 20 минут', 'за 20 минут'],
    [3_600, 'около 1 часа', 'за 1 час'],
    [10_800, 'около 3 часов', 'за 3 часа'],
    [18_000, 'около 5 часов', 'за 5 часов'],
  ])('%d seconds — «%s», «%s»', (seconds, about, took) => {
    expect(plain(aboutTimeWords(seconds))).toBe(about);
    expect(plain(tookTimeWords(seconds))).toBe(took);
  });
});
