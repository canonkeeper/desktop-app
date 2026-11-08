import type { AccountQuota } from '../../entities/account-quota/api';
import { quotaNearLimit } from '../../entities/account-quota/api';
import { pluralize } from '../../ui/copy';

/** Questions left today, shown from 80 % of the daily limit (quota spec R5.3). */
export function chatQuotaHint(quota: AccountQuota | undefined) {
  if (!quota || !quotaNearLimit(quota.chatTurns)) return '';
  const { used, limit } = quota.chatTurns;
  const left = Math.max(0, limit - used);
  return `Сегодня ${pluralize(left, 'остался', 'осталось', 'осталось')} ${left} ${pluralize(left, 'вопрос', 'вопроса', 'вопросов')} из ${limit}. Счётчик обновится в 00:00 по Москве.`;
}

/** The refusal of a new question by its quota code (R5.3, R5.4); '' for any other code. */
export function chatQuotaRefusal(code: string, quota: AccountQuota | undefined) {
  if (code === 'quota.chat_turns_daily') {
    const limit = quota?.chatTurns.limit;
    return limit === undefined
      ? 'Сегодня задан дневной лимит вопросов. Новые вопросы можно задать после 00:00 по Москве.'
      : `Сегодня ${pluralize(limit, 'задан', 'задано', 'задано')} ${limit} ${pluralize(limit, 'вопрос', 'вопроса', 'вопросов')} — это дневной лимит. Новые вопросы можно задать после 00:00 по Москве.`;
  }
  if (code === 'quota.active_turns') {
    const limit = quota?.activeTurns.limit;
    return limit === undefined
      ? 'Сейчас готовятся ответы на ваши вопросы. Новый вопрос можно задать, когда один из них будет готов.'
      : `Сейчас ${pluralize(limit, 'готовится ответ', 'готовятся ответы', 'готовятся ответы')} на ${limit} ${pluralize(limit, 'ваш вопрос', 'ваших вопроса', 'ваших вопросов')}. Новый вопрос можно задать, когда ${limit === 1 ? 'он' : 'один из них'} будет готов.`;
  }
  return '';
}
