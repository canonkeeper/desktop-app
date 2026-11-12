import { describe, expect, it } from 'vitest';
import { goesIn, returnedPlace, trashTitle } from './recently-deleted-words';

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
    expect(trashTitle({ kind: 'chapter', item: { ...at, id: 'c', projectId: 'p', sagaTitle: 'С', bookId: 'b', bookTitle: 'Б', bookDisplayNumber: '2', title: 'Белый порт', order: 12 } }))
      .toBe('Глава 12 «Белый порт»');
    expect(trashTitle({ kind: 'drawing', item: { ...at, id: 'd', projectId: 'p', sagaTitle: 'С', title: ' ' } })).toBe('Рисунок без названия');
    expect(returnedPlace({ kind: 'book', item: { ...at, id: 'b', projectId: 'p', sagaTitle: 'С', title: 'Т' } }))
      .toEqual({ path: '/manuscript/books', query: { projectId: 'p', bookId: 'b' } });
  });
});
