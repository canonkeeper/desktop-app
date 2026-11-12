import { describe, expect, it } from 'vitest';
import type { AccountQuota } from '../../entities/account-quota/api';
import { refusalWords } from '../../entities/account-quota/refusal-words';
import { chatLimitNotice, chatQuotaHint, questionLengthHint } from './chat-quota';

const counter = (used: number, limit: number) => ({ used, limit });
const nbsp = String.fromCharCode(0xa0);
const sources = { books: counter(0, 1), chapters: counter(0, 1), characters: counter(0, 1) };
function quota(used: number, limit = 30, active = 2): AccountQuota {
  return {
    day: '2026-10-28', resetsAt: '2026-10-28T21:00:00Z', chatTurns: counter(used, limit),
    activeTurns: counter(0, active), imports: counter(0, 30), images: counter(0, 30),
    imageUploads: counter(0, 30), chapterDrafts: counter(0, 20), drawingTurns: counter(0, 60), draftTurns: counter(0, 60), draftRewrites: counter(0, 200),
    projects: counter(1, 3), account: sources, ownedProjects: [],
  };
}

// specs/2026-11-10-ux1-chat.md CH3, CH5: the count and the price stand at the field before the author presses.
describe('the line above the question field', () => {
  it('counts the questions left from the first one while the day gives no more than 10', () => {
    expect(chatQuotaHint(quota(0, 5))).toBe('Осталось 5 из 5 вопросов на сегодня.');
    expect(chatQuotaHint(quota(1, 5))).toBe('Осталось 4 из 5 вопросов на сегодня.');
    expect(chatQuotaHint(quota(4, 5))).toBe('Остался 1 из 5 вопросов на сегодня. Новые — после 00:00 по Москве.');
    expect(chatQuotaHint(quota(2, 10))).toBe('Осталось 8 из 10 вопросов на сегодня.');
    expect(chatQuotaHint(undefined)).toBe('');
  });

  it('counts from 80 % of a larger day (quota spec R5.3)', () => {
    expect(chatQuotaHint(quota(23))).toBe('');
    expect(chatQuotaHint(quota(24))).toBe('Осталось 6 из 30 вопросов на сегодня. Новые — после 00:00 по Москве.');
    expect(chatQuotaHint(quota(29))).toBe('Остался 1 из 30 вопросов на сегодня. Новые — после 00:00 по Москве.');
  });

  it('says that a correction counts as a question', () => {
    expect(chatQuotaHint(quota(2, 5), 'correction')).toBe('Уточнение к идущему ответу — считается как вопрос. Осталось 3 из 5 на сегодня.');
    expect(chatQuotaHint(quota(4, 5), 'correction')).toBe('Уточнение к идущему ответу — считается как вопрос. Остался 1 из 5 на сегодня.');
    expect(chatQuotaHint(quota(3), 'correction')).toBe('Уточнение к идущему ответу — считается как вопрос.');
    expect(chatQuotaHint(undefined, 'correction')).toBe('Уточнение к идущему ответу — считается как вопрос.');
  });

  it('says when a correction will be taken while the question waits in the queue', () => {
    expect(chatQuotaHint(quota(2, 5), 'waiting')).toBe('Уточнить можно будет, когда помощник начнёт.');
  });

  it('gives way to the block once nothing is left', () => {
    expect(chatQuotaHint(quota(5, 5))).toBe('');
    expect(chatQuotaHint(quota(6, 5), 'correction')).toBe('');
  });

  it('counts the signs of a long question (CH1.7)', () => {
    expect(questionLengthHint('а'.repeat(6_999), false)).toBe('');
    // The groups of a number are kept together, as everywhere on the page.
    expect(questionLengthHint('а'.repeat(7_214), false)).toBe(`7${nbsp}214 из 8${nbsp}000 знаков. Длинный текст приложите главой.`);
    expect(questionLengthHint('𝒜'.repeat(7_000), true)).toBe(`7${nbsp}000 из 8${nbsp}000 знаков.`);
  });
});

describe('the one block of a refusal by the limit', () => {
  it('says what happens to the question; what is over and when it comes back are the words of the one table', () => {
    expect(chatLimitNotice({ quota: quota(5, 5), refusedCode: 'quota.chat_turns_daily', turnActive: false })).toEqual({
      code: 'quota.chat_turns_daily', refused: true, kept: 'Ваш вопрос остался в поле — задайте его завтра.',
    });
    expect(refusalWords('quota.chat_turns_daily', quota(5, 5))).toEqual({
      ended: 'Вопросы и уточнения на сегодня закончились — 5 из 5', returns: 'Новые — после 00:00 по Москве.',
    });
    // The quota is not read yet: the refusal names no number.
    expect(chatLimitNotice({ quota: undefined, refusedCode: 'quota.chat_turns_daily', turnActive: false })?.refused).toBe(true);
    expect(refusalWords('quota.chat_turns_daily', undefined)?.ended).toBe('Вопросы и уточнения на сегодня закончились');
  });

  it('stands before the author presses once nothing is left', () => {
    expect(chatLimitNotice({ quota: quota(5, 5), refusedCode: '', turnActive: false })).toEqual({
      code: 'quota.chat_turns_daily', refused: false, kept: 'Вопрос можно набрать уже сейчас — он сохранится в поле.',
    });
    expect(chatLimitNotice({ quota: quota(4, 5), refusedCode: '', turnActive: false })).toBeNull();
    expect(chatLimitNotice({ quota: undefined, refusedCode: '', turnActive: false })).toBeNull();
  });

  it('says that the answer of a running turn still comes', () => {
    expect(chatLimitNotice({ quota: quota(5, 5), refusedCode: 'quota.chat_turns_daily', turnActive: true })?.kept)
      .toBe('Ответ на ваш вопрос готовится и придёт. Текст уточнения остался в поле.');
    expect(chatLimitNotice({ quota: quota(5, 5), refusedCode: '', turnActive: true })?.kept)
      .toBe('Ответ на ваш вопрос готовится и придёт.');
  });

  it('names the questions at once without a day or a tier (quota-accounting Q1.5)', () => {
    expect(chatLimitNotice({ quota: quota(3), refusedCode: 'quota.active_turns', turnActive: false })).toEqual({
      code: 'quota.active_turns', refused: true, kept: 'Ваш вопрос остался в поле.',
    });
    expect(refusalWords('quota.active_turns', quota(3))).toEqual({
      ended: 'Сейчас готовятся ответы на 2 ваших вопроса', returns: 'Новый вопрос можно задать, когда один из них будет готов.',
      // It passes by itself when an answer is ready: the block has no «Написать нам» (AA2.7).
      passes: true,
    });
    expect(refusalWords('quota.active_turns', quota(3, 30, 1))).toEqual({
      ended: 'Сейчас готовится ответ на 1 ваш вопрос', returns: 'Новый вопрос можно задать, когда он будет готов.', passes: true,
    });
    expect(refusalWords('quota.active_turns', undefined)?.ended).toBe('Сейчас готовятся ответы на ваши вопросы');
  });

  it('leaves any other refusal to the block of the feed', () => {
    expect(chatLimitNotice({ quota: quota(3), refusedCode: 'quota.account_characters', turnActive: false })).toBeNull();
    expect(chatLimitNotice({ quota: quota(3), refusedCode: 'rate_limit.exceeded', turnActive: false })).toBeNull();
  });
});
