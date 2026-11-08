import { describe, expect, it } from 'vitest';
import { formatReaderChapterTitle } from '../../../ui/copy';

describe('formatReaderChapterTitle', () => {
  it('removes only the exact duplicate current chapter ordinal', () => {
    expect(formatReaderChapterTitle('12', 'Глава 12. Белый порт')).toBe('Белый порт');
    expect(formatReaderChapterTitle('12', 'Глава 12.')).toBe('');
    expect(formatReaderChapterTitle('12', 'Белый порт')).toBe('Белый порт');
    expect(formatReaderChapterTitle('12', 'Глава 12.1: Белый порт')).toBe('Глава 12.1: Белый порт');
    expect(formatReaderChapterTitle('12', 'Глава 13. Белый порт')).toBe('Глава 13. Белый порт');
  });
});
