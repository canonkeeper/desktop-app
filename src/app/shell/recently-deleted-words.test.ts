import { describe, expect, it } from 'vitest';
import { goesIn, returnedPlace, returnedWords, trashTitle } from './recently-deleted-words';

const hour = 3_600_000;

// specs/2026-11-11-ux7-trash.md TB1.1: the term in words — 7 days right after the deletion, the last day, the end.
describe('the words of «Недавно удалённые»', () => {
  it('says when a row goes for good', () => {
    const now = Date.parse('2026-11-11T09:00:00Z');
    const until = (left: number) => new Date(now + left).toISOString();
    expect(goesIn(until(7 * 24 * hour - 60_000), now)).toBe('исчезнет через 7 дней');
    expect(goesIn(until(5 * 24 * hour - hour), now)).toBe('исчезнет через 5 дней');
    expect(goesIn(until(25 * hour), now)).toBe('исчезнет через 2 дня');
    expect(goesIn(until(3 * hour), now)).toBe('исчезнет в течение суток');
    expect(goesIn(until(-1), now)).toBe('срок вышел');
  });

  it('names a row and its place', () => {
    const at = { deletedAt: '2026-11-11T09:00:00Z', restoreUntil: '2026-11-18T09:00:00Z' };
    // A deleted chapter has no place among the living ones: the number is the one its title writes, or none
    // (specs/2026-11-15-reader-chapter-number.md RN1.4); «Вернуть» says it in the same words.
    const chapter = { ...at, id: 'c', projectId: 'p', sagaTitle: 'С', bookId: 'b', bookTitle: 'Б', bookDisplayNumber: '2' };
    const numbered = { kind: 'chapter', item: { ...chapter, title: 'Глава 12. Белый порт', displayNumber: '12', displayTitle: 'Белый порт' } } as const;
    expect(trashTitle(numbered)).toBe('Глава 12 «Белый порт»');
    expect(returnedWords(numbered)).toEqual({ message: 'Глава 12 «Белый порт» возвращена', detail: 'Книга 2 «Б»' });
    const untitled = { kind: 'chapter', item: { ...chapter, title: 'Сигнальные огни', displayNumber: '', displayTitle: 'Сигнальные огни' } } as const;
    expect(trashTitle(untitled)).toBe('Глава «Сигнальные огни»');
    expect(returnedWords(untitled)).toEqual({ message: 'Глава «Сигнальные огни» возвращена', detail: 'Книга 2 «Б»' });
    expect(trashTitle({ kind: 'chapter', item: { ...chapter, title: 'Глава 12.', displayNumber: '12', displayTitle: '' } })).toBe('Глава 12');
    expect(trashTitle({ kind: 'drawing', item: { ...at, id: 'd', projectId: 'p', sagaTitle: 'С', title: ' ' } })).toBe('Рисунок без названия');
    expect(returnedPlace({ kind: 'book', item: { ...at, id: 'b', projectId: 'p', sagaTitle: 'С', title: 'Т' } }))
      .toEqual({ path: '/manuscript/books', query: { projectId: 'p', bookId: 'b' } });
  });
});
