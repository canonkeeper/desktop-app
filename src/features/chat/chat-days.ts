import type { ChatSession } from '../../entities/chat/api';

/**
 * The chats of a saga by the time of their last question (specs/2026-11-11-ux4-after-answer.md AA2.1, AA3.1): the latest
 * conversation «Чат» opens, the days of the list and the date of a row. A chat with no question yet stands by the time it
 * was made or renamed. The days are those of the author's clock.
 */
type Dated = Pick<ChatSession, 'id' | 'updatedAt' | 'latestTurnAt'>;

export function askedAt(chat: Dated) {
  return Date.parse(chat.latestTurnAt ?? chat.updatedAt);
}

/** The chat asked last; none for a saga without chats. */
export function latestChat<T extends Dated>(chats: readonly T[]): T | undefined {
  return [...chats].sort((left, right) => askedAt(right) - askedAt(left))[0];
}

export type ChatDay = 'today' | 'yesterday' | 'week' | 'earlier';

export const chatDayWords: Readonly<Record<ChatDay, string>> = {
  today: 'Сегодня', yesterday: 'Вчера', week: 'На этой неделе', earlier: 'Раньше',
};

function startOfDay(at: Date) {
  return new Date(at.getFullYear(), at.getMonth(), at.getDate());
}

/** The day of a chat by the calendar of the author; the week starts on Monday. */
export function chatDay(at: Date, now: Date): ChatDay {
  const today = startOfDay(now);
  if (at >= today) return 'today';
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
  if (at >= yesterday) return 'yesterday';
  const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - ((today.getDay() + 6) % 7));
  return at >= monday ? 'week' : 'earlier';
}

/** The groups of the list, newest first; an empty group is not there. */
export function chatsByDay<T extends Dated>(chats: readonly T[], now: Date): { day: ChatDay; chats: T[] }[] {
  const sorted = [...chats].sort((left, right) => askedAt(right) - askedAt(left));
  const groups: { day: ChatDay; chats: T[] }[] = [];
  for (const chat of sorted) {
    const day = chatDay(new Date(askedAt(chat)), now);
    if (groups.at(-1)?.day !== day) groups.push({ day, chats: [] });
    groups.at(-1)!.chats.push(chat);
  }
  return groups;
}

/** «12 ноября», with the year when it is not this one. */
export function chatDate(at: Date, now: Date) {
  return at.toLocaleDateString('ru-RU', at.getFullYear() === now.getFullYear()
    ? { day: 'numeric', month: 'long' }
    : { day: 'numeric', month: 'long', year: 'numeric' }).replace(/\s*г\.$/u, '');
}
