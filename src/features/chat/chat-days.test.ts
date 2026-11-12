import { describe, expect, it } from 'vitest';
import { chatDate, chatDay, chatsByDay, latestChat } from './chat-days';

// specs/2026-11-11-ux4-after-answer.md AA3.1: the days of the list by the author's clock, at the bounds of a day.
describe('the days of the chats', () => {
  // Wednesday, 12 November 2026, 00:30 by the clock of the author.
  const now = new Date(2026, 10, 12, 0, 30);
  const at = (day: number, hour: number, minute = 0) => new Date(2026, 10, day, hour, minute);

  it('puts a chat into today, yesterday, this week or earlier at the bounds of a day', () => {
    expect(chatDay(at(12, 0, 0), now)).toBe('today');
    expect(chatDay(at(11, 23, 59), now)).toBe('yesterday');
    expect(chatDay(at(11, 0, 0), now)).toBe('yesterday');
    expect(chatDay(at(10, 23, 59), now)).toBe('week');
    // The week starts on Monday, 9 November.
    expect(chatDay(at(9, 0, 0), now)).toBe('week');
    expect(chatDay(at(8, 23, 59), now)).toBe('earlier');
  });

  it('groups the chats newest first by their last question, and a chat without one by its own time', () => {
    const chats = [
      { id: 'a', updatedAt: at(1, 9).toISOString(), latestTurnAt: at(12, 0, 10).toISOString() },
      { id: 'b', updatedAt: at(11, 22).toISOString() },
      { id: 'c', updatedAt: at(2, 9).toISOString(), latestTurnAt: at(3, 9).toISOString() },
      { id: 'd', updatedAt: at(10, 9).toISOString(), latestTurnAt: at(10, 12).toISOString() },
    ];
    expect(chatsByDay(chats, now).map((group) => [group.day, group.chats.map((chat) => chat.id)])).toEqual([
      ['today', ['a']], ['yesterday', ['b']], ['week', ['d']], ['earlier', ['c']],
    ]);
    expect(latestChat(chats)?.id).toBe('a');
    expect(latestChat([])).toBeUndefined();
  });

  it('writes the date as the author reads it', () => {
    expect(chatDate(at(12, 9), now)).toBe('12 ноября');
    expect(chatDate(new Date(2025, 0, 3), now)).toBe('3 января 2025');
  });
});
