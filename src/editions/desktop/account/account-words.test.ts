// SPDX-License-Identifier: AGPL-3.0-only
import { afterEach, describe, expect, it, vi } from 'vitest';
import { hiddenIn } from '../../../shared/copy/dictionary';
import { chooseWords } from '../settings/helper/choose-helper';
import { connectWords } from '../settings/helper/ConnectWindow';
import { sourceWords } from '../settings/helper/SourceChoice';
import { accountWords, computerLine, failureLine, freshLine, linkedLine, offlineLine, readingLine, tierLine, waitingLine } from './account-words';
import { helperReasonOf } from './helper-reason';

const at = Date.parse('2026-11-15T11:05:00Z');

/**
 * The words of Data of specs/2026-11-15-desk-account-link-web.md in the file each stands in: the dictionary of the pages
 * (src/shared/copy/dictionary.ts) hides none of them there — «ИИ» and «ключ» stand only in the part «Помощник».
 */
const places: Record<string, readonly string[]> = {
  'src/editions/desktop/account/account-words.ts': [
    ...Object.values(accountWords),
    waitingLine(new Date(at + 9.5 * 60_000).toISOString(), at), computerLine('Ноутбук'),
    ...(['expired', 'exchange'] as const).map((failure) => failureLine(failure, 'system')),
    failureLine('keyring', 'system'), failureLine('keyring', 'file'),
    linkedLine('a…@example.com', 'account'), linkedLine('a…@example.com', 'own'), freshLine(new Date(at).toISOString(), at),
    offlineLine(new Date(at).toISOString(), at), tierLine({ code: 'writer', title: 'Писатель', until: '2026-12-14T00:00:00Z' }),
    readingLine({ used: 3_200_000, limit: 40_000_000 }),
  ],
  'src/editions/desktop/account/helper-reason.ts': [
    helperReasonOf('account', 'broken').title, helperReasonOf('account', 'waiting').title, helperReasonOf('account', 'none').title,
  ],
  'src/editions/desktop/settings/helper/ConnectWindow.tsx': Object.values(connectWords),
  'src/editions/desktop/settings/helper/choose-helper.ts': Object.values(chooseWords),
  'src/editions/desktop/settings/helper/SourceChoice.tsx': [
    sourceWords.legend, sourceWords.linkFirst, sourceWords.chatModel('gpt-north-chat'), sourceWords.cardsModel('gpt-north-cards'),
  ],
};

describe('the words of the link with an account and of the choice of the helper', () => {
  it.each(Object.entries(places))('hide no word of the dictionary in %s', (path, words) => {
    expect(words.flatMap((words) => hiddenIn(words, path).map((hidden) => `${words}: ${hidden.says}`))).toEqual([]);
  });

  it('keep «Свой ИИ» out of the files of the account: the dictionary hides it there', () => {
    expect(hiddenIn(connectWords.own, 'src/editions/desktop/account/account-words.ts').length).toBeGreaterThan(0);
  });
});

// AW2.2: the end of the tier is a day of the Moscow calendar, as the site writes it (src/features/tiers/tiers-copy.ts) — the
// same day whatever the zone of the computer. The zone of the computer is what a format without its own zone takes; the
// workers of the tests keep the zone they started with, so the test gives it to such a format itself.
describe('the end of the tier', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it.each(['UTC', 'America/New_York', 'Europe/Moscow'])('is the day of Moscow on a computer in %s', async (zone) => {
    const Format = Intl.DateTimeFormat;
    vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(function inTheZone(locales?: string | string[], options?: Intl.DateTimeFormatOptions) {
      return new Format(locales, { timeZone: zone, ...options });
    } as unknown as typeof Intl.DateTimeFormat);
    vi.resetModules();
    const words = await import('./account-words');
    // 00:30 of 14 December in Moscow is still 13 December in UTC and in New York.
    expect(words.tierLine({ code: 'writer', title: 'Писатель', until: '2026-12-13T21:30:00Z' })).toBe('Тариф «Писатель» до 14 декабря');
  });
});
