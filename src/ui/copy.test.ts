import { describe, expect, it } from 'vitest';
import { formatCount, formatFileSize, formatProjectMeta, formatRelativeTime } from './copy';

describe('project copy', () => {
  it.each([
    [1, '1 слово'],
    [2, '2 слова'],
    [999, '999 слов'],
    [1_000, '1 тыс. слов'],
    [1_250, '1,3 тыс. слов'],
    [96_000, '96 тыс. слов'],
    [233_000, '233 тыс. слов'],
    [999_999, '1 000 тыс. слов'],
    [1_000_000, '1 млн слов'],
    [1_250_000, '1,3 млн слов'],
  ])('formats %i words without changing the other project counts', (wordCount, words) => {
    expect(formatProjectMeta({ bookCount: 4, chapterCount: 53, wordCount })).toBe(`4 книги · 53 главы · ${words}`);
  });

  it('keeps the missing-project copy', () => {
    expect(formatProjectMeta(null)).toBe('');
    expect(formatProjectMeta(undefined)).toBe('');
  });
});

describe('writer counts (R5.3)', () => {
  it.each([
    [1, '1 заметка', '1 глава', '1 книга'],
    [2, '2 заметки', '2 главы', '2 книги'],
    [5, '5 заметок', '5 глав', '5 книг'],
    [11, '11 заметок', '11 глав', '11 книг'],
    [21, '21 заметка', '21 глава', '21 книга'],
  ])('declines %i notes, chapters and books', (count, notes, chapters, books) => {
    expect(formatCount(count, 'заметка', 'заметки', 'заметок')).toBe(notes);
    expect(formatCount(count, 'глава', 'главы', 'глав')).toBe(chapters);
    expect(formatCount(count, 'книга', 'книги', 'книг')).toBe(books);
  });

  it('writes sizes in МБ with a decimal comma', () => {
    expect(formatFileSize(1.2 * 1024 * 1024)).toBe('1,2 МБ');
    expect(formatFileSize(512)).toBe('512 Б');
    // The pass on TEST 11.11: a file of 4 КБ said «0,0 МБ»; below 0,1 МБ the size is in kilobytes.
    expect(formatFileSize(4 * 1024)).toBe('4 КБ');
    expect(formatFileSize(100 * 1024)).toBe('100 КБ');
    expect(formatFileSize(0.1 * 1024 * 1024)).toBe('0,1 МБ');
  });
});

describe('relative time (R5.3)', () => {
  const now = new Date(2026, 9, 28, 10, 0).getTime();

  it('counts minutes and hours within a day', () => {
    expect(formatRelativeTime(new Date(2026, 9, 28, 9, 59, 40), now)).toBe('только что');
    expect(formatRelativeTime(new Date(2026, 9, 28, 9, 55), now)).toBe('5 минут назад');
    expect(formatRelativeTime(new Date(2026, 9, 28, 7, 0), now)).toBe('3 часа назад');
  });

  it('names yesterday, a date of this year and a date of another year', () => {
    expect(formatRelativeTime(new Date(2026, 9, 27, 9, 20), now)).toBe('вчера в 09:20');
    expect(formatRelativeTime(new Date(2026, 9, 12, 14, 20), now)).toBe('12 окт. в 14:20');
    expect(formatRelativeTime(new Date(2025, 9, 12, 14, 20), now)).toBe('12 окт. 2025');
  });

  it('calls the last day of the previous year yesterday', () => {
    expect(formatRelativeTime(new Date(2026, 11, 31, 8, 0), new Date(2027, 0, 1, 9, 0).getTime())).toBe('вчера в 08:00');
  });
});
