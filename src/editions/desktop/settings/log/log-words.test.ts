// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { Schema } from '../../../../shared/api';
import { dayWords, ownWords, thousandsOfTokens } from './log-words';

type Own = NonNullable<Schema<'SendLogRow'>['own']>;

const table = JSON.parse(readFileSync(join(process.cwd(), 'backend/internal/domain/modeljournal/testdata/own-words.json'), 'utf8')) as {
  cases: Array<{ own: Own; want: string }>;
};

// specs/2026-11-14-desk-journal.md JR3.2, Data «подробности: числа «Своего ИИ»»: the details of the page and the file of the
// journal say the numbers by one table; a thousand thousands of tokens is a million, never «1000 тыс.».
describe('the words of «Журнал»', () => {
  it('says the numbers of «Свой ИИ» by the table of the file', () => {
    expect(table.cases.length).toBeGreaterThanOrEqual(10);
    for (const example of table.cases) expect(ownWords(example.own), JSON.stringify(example.own)).toBe(example.want);
  });

  it('counts the tokens up to a million and from it', () => {
    const plain = (tokens: number) => thousandsOfTokens(tokens).replace(/\u00a0/gu, ' ');
    expect(plain(0)).toBe('0 токенов');
    expect(plain(1)).toBe('1 токен');
    expect(plain(999)).toBe('999 токенов');
    expect(plain(1_000)).toBe('1 тыс. токенов');
    expect(plain(999_499)).toBe('999 тыс. токенов');
    expect(plain(999_500)).toBe('1 млн токенов');
    expect(plain(999_600)).toBe('1 млн токенов');
    expect(plain(1_200_000)).toBe('1,2 млн токенов');
  });

  it('names the days of the computer whatever the date', () => {
    const now = new Date(2027, 0, 3, 10, 0);
    expect(dayWords(new Date(2027, 0, 3, 0, 30).toISOString(), now)).toBe('Сегодня, 3 января');
    expect(dayWords(new Date(2027, 0, 2, 23, 30).toISOString(), now)).toBe('Вчера, 2 января');
    expect(dayWords(new Date(2027, 0, 1, 0, 30).toISOString(), now)).toBe('1 января');
    expect(dayWords(new Date(2026, 11, 29, 0, 30).toISOString(), now)).toBe('29 декабря 2026');
  });
});
