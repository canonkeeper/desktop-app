import type { AccountQuota } from '../../entities/account-quota/api';
import { quotaNearLimit } from '../../entities/account-quota/api';
import { formatCount, pluralize } from '../../ui/copy';
import { questionLimit, signs } from './chat-chapter';

/**
 * The count and the price of a message of the chat in the words of the author (specs/2026-11-10-ux1-chat.md CH3). The
 * numbers are the server's (`getAccountQuota`): a question and a correction spend the same unit of the day
 * (specs/2026-11-09-quota-accounting.md Q2.1), the page counts nothing itself.
 */

/** A day of up to this many questions shows its counter from the first one (CH3.1). */
const countedDay = 10;
const comesBack = 'Новые — после 00:00 по Москве.';

/** What the field is for now: a question, a correction of the running turn, or a correction the queue does not take yet. */
export type ChatFieldMode = 'question' | 'correction' | 'waiting';

/**
 * The line above the question field (CH3.1, CH3.2, CH5.1): the questions left today — always on a short day, from 80 %
 * of a longer one (quota spec R5.3) — and that a correction counts as a question. Nothing is left: the block of the
 * limit stands instead.
 */
export function chatQuotaHint(quota: AccountQuota | undefined, mode: ChatFieldMode = 'question') {
  if (mode === 'waiting') return 'Уточнить можно будет, когда помощник начнёт.';
  const turns = quota?.chatTurns;
  const left = turns && turns.limit > 0 ? turns.limit - turns.used : null;
  if (left !== null && left <= 0) return '';
  const near = quotaNearLimit(turns);
  const counted = turns && left !== null && (turns.limit <= countedDay || near)
    ? `${pluralize(left, 'Остался', 'Осталось', 'Осталось')} ${left} из ${turns.limit}` : '';
  // The line stands over the field while the turn runs and does not go with the first letter (specs/2026-11-10-ux4-chat-waiting.md CW2.4).
  if (mode === 'correction') return counted ? `Уточнение к идущему ответу — считается как вопрос. ${counted} на сегодня.` : 'Уточнение к идущему ответу — считается как вопрос.';
  if (!counted || !turns) return '';
  const questions = pluralize(turns.limit, 'вопроса', 'вопросов', 'вопросов');
  return `${counted} ${questions} на сегодня.${near ? ` ${comesBack}` : ''}`;
}

/** The signs of a question near its limit (CH1.7); a correction takes no chapter. */
export function questionLengthHint(text: string, correction: boolean) {
  const count = signs(text);
  if (count < questionLimit - 1_000) return '';
  // «7 214 из 8 000 знаков»: the groups of a number as the rest of the page writes counts.
  const counted = `${formatCount(count, '', '', '').trimEnd()} из ${formatCount(questionLimit, 'знака', 'знаков', 'знаков')}.`;
  return correction ? counted : `${counted} Длинный текст приложите главой.`;
}

/** The one block of a refusal by a limit above the field (CH3.3–CH3.5). */
export interface ChatLimitNoticeCopy {
  /** The code of the limit: the words of the block and what a build adds (the tier of the site) go by it. */
  code: 'quota.chat_turns_daily' | 'quota.active_turns';
  /** The server refused a message; false while the block only warns that nothing is left. */
  refused: boolean;
  /** What became of the typed text (CH3.4): the place's own line of the block. */
  kept: string;
}

interface ChatLimitInput {
  quota: AccountQuota | undefined;
  /** The `quota.*` code of the last refused message; '' without one. */
  refusedCode: string;
  /** A turn of this chat works: its answer still comes. */
  turnActive: boolean;
}

/**
 * Which block stands: the questions and corrections of the day are over — after a refusal, or before the author presses
 * once the quota shows none left — or the answers being prepared hold a new question back. Any other refusal keeps the
 * block of the feed. What ended and when it comes back are the words of the one table
 * (specs/2026-11-10-ux2-patterns-refusals.md PR2.1); the chat says only what became of the text.
 */
export function chatLimitNotice({ quota, refusedCode, turnActive }: ChatLimitInput): ChatLimitNoticeCopy | null {
  if (refusedCode === 'quota.active_turns') return { code: 'quota.active_turns', refused: true, kept: 'Ваш вопрос остался в поле.' };
  const turns = quota?.chatTurns;
  const refused = refusedCode === 'quota.chat_turns_daily';
  if (!refused && !(turns && turns.limit > 0 && turns.used >= turns.limit)) return null;
  // What happens to the text: the refused one stayed in the field; before a refusal the field still takes one.
  const kept = turnActive
    ? `Ответ на ваш вопрос готовится и придёт.${refused ? ' Текст уточнения остался в поле.' : ''}`
    : refused ? 'Ваш вопрос остался в поле — задайте его завтра.' : 'Вопрос можно набрать уже сейчас — он сохранится в поле.';
  return { code: 'quota.chat_turns_daily', refused, kept };
}
