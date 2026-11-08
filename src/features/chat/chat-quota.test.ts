import { describe, expect, it } from 'vitest';
import type { AccountQuota } from '../../entities/account-quota/api';
import { chatQuotaHint, chatQuotaRefusal } from './chat-quota';

const counter = (used: number, limit: number) => ({ used, limit });
const sources = { books: counter(0, 1), chapters: counter(0, 1), characters: counter(0, 1) };
function quota(used: number, limit = 30, active = 2): AccountQuota {
  return {
    day: '2026-10-28', resetsAt: '2026-10-28T21:00:00Z', chatTurns: counter(used, limit),
    activeTurns: counter(0, active), imports: counter(0, 30), images: counter(0, 30), projects: counter(1, 3), account: sources, ownedProjects: [],
  };
}

describe('chat quota texts (quota spec R5.3)', () => {
  it('shows questions left from 80 % of the day', () => {
    expect(chatQuotaHint(quota(23))).toBe('');
    expect(chatQuotaHint(quota(24))).toBe('Сегодня осталось 6 вопросов из 30. Счётчик обновится в 00:00 по Москве.');
    expect(chatQuotaHint(quota(29))).toBe('Сегодня остался 1 вопрос из 30. Счётчик обновится в 00:00 по Москве.');
  });

  it('names the limits in refusals', () => {
    expect(chatQuotaRefusal('quota.chat_turns_daily', quota(30))).toBe(
      'Сегодня задано 30 вопросов — это дневной лимит. Новые вопросы можно задать после 00:00 по Москве.',
    );
    expect(chatQuotaRefusal('quota.active_turns', quota(3))).toBe(
      'Сейчас готовятся ответы на 2 ваших вопроса. Новый вопрос можно задать, когда один из них будет готов.',
    );
    expect(chatQuotaRefusal('quota.active_turns', quota(3, 30, 1))).toBe(
      'Сейчас готовится ответ на 1 ваш вопрос. Новый вопрос можно задать, когда он будет готов.',
    );
    expect(chatQuotaRefusal('rate_limit.exceeded', quota(3))).toBe('');
  });
});
