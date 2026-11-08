import { useEffect, useState } from 'react';
import { quotaFailureCode } from '../../entities/account-quota/api';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import type { ChatJob } from '../../entities/chat/api';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { formatRetryAfterSeconds, retryAfterSeconds } from './research-chat-lifecycle';

/**
 * One error block of the chat (UC4.2): what happened, what to do, the way out and the support
 * code. `ask_again` is the composer's «Спросить снова» with the question kept in the field, so
 * the block itself has no second retry; `retry` repeats a failed read or stop with «Повторить».
 */
export interface ChatFailure {
  title?: string;
  text: string;
  action?: 'ask_again' | 'retry' | 'new_chat' | 'books';
  supportCode?: string;
  /** A `quota.*` refusal of a question: the panel names the limit from the account quota. */
  quotaCode?: string;
}

/** A chat of a previous assistant version keeps its history but takes no new questions. */
export function outdatedChatMessage() {
  return 'Этот чат вёл прежний помощник: история сохранена, но продолжить его нельзя. Задайте вопрос в новом чате.';
}

// Data «Отказы хода»: the codes of a failed turn Job and of a refused question.
const failuresByCode: Readonly<Record<string, ChatFailure>> = {
  'model.usage_limit_exceeded': {
    title: 'Модель сейчас недоступна',
    text: 'Исчерпан общий лимит обращений к модели — это не связано с вашим вопросом или книгами. Вопрос сохранён в поле: отправьте его позже.',
  },
  'model.server_overloaded': { title: 'Модель перегружена', text: 'Подождите пару минут и спросите снова — вопрос сохранён.', action: 'ask_again' },
  'model.http_connection_failed': {
    title: 'Нет связи с моделью', text: 'Связь с сервисом модели прервалась. Спросите снова через несколько минут.', action: 'ask_again',
  },
  'model.context_window_exceeded': {
    title: 'Чат стал слишком длинным',
    text: 'Модель больше не удерживает этот разговор целиком. Начните новый чат — книги и прежние ответы никуда не денутся.',
    action: 'new_chat',
  },
  'model.turn_interrupted': {
    title: 'Ответ прерван', text: 'Ответ остановили или сервис перезапускался. Спросите снова — вопрос сохранён.', action: 'ask_again',
  },
  start_indexing: { title: 'Книга ещё не подготовлена', text: 'Откройте «Мои книги» и запустите подготовку.', action: 'books' },
  'chat.outdated_agent': { text: outdatedChatMessage() },
  // A correction (specs/2026-10-30-chat-steering.md R4.3): the text stays in the field.
  'chat.turn_ended': { title: 'Ход уже завершился', text: 'Текст остался в поле — отправьте его новым вопросом.', action: 'ask_again' },
  'chat.steer_retry': { text: 'Сейчас уточнение не принимается. Текст остался в поле — повторите через несколько секунд.' },
  'chat.steer_limit': { text: 'В этот ход отправлено слишком много уточнений. Дождитесь ответа — текст останется в поле.' },
};

const unansweredTurn: ChatFailure = {
  title: 'Не удалось получить ответ',
  text: 'Спросите снова. Если ошибка повторится — напишите в поддержку, код уже в письме.',
  action: 'ask_again',
};

/** UC4.2: the block under a turn whose Job failed, by the code of the Job (R4.2.3, R4.2.4). */
export function turnFailure(error: ChatJob['error']): ChatFailure {
  const failure = (error?.code && failuresByCode[error.code]) || unansweredTurn;
  return error?.requestId ? { ...failure, supportCode: error.requestId } : failure;
}

/** The block of a lost timeline stream while the turn works: it recovers by itself (R4.2.7). */
export function streamFailure(closed: boolean): ChatFailure {
  return closed
    ? { title: 'Связь прервалась', text: 'Связь потеряна. Ответ продолжает готовиться и появится, когда связь вернётся.', action: 'retry' }
    : { title: 'Связь прервалась', text: 'Связь прервалась — восстанавливаем. Ответ продолжает готовиться на сервере.' };
}

function sendFailure(error: unknown, supportCode: string | undefined, steering = false): ChatFailure {
  const code = isApiStatusFailure(error) ? error.problem.code : undefined;
  const known = code ? failuresByCode[code] : undefined;
  const unavailable = 'Сервис временно недоступен. Вопрос сохранён — отправьте его ещё раз через минуту.';
  const failure = known ?? (steering ? {
    title: 'Уточнение не отправлено',
    text: publicApiErrorMessage(error, 'Текст остался в поле — отправьте его ещё раз.', {
      forbidden: 'Нет прав задавать вопросы в этой саге.',
      network: 'Сервис временно недоступен. Текст остался в поле — отправьте его ещё раз.',
      server: 'Сервис временно недоступен. Текст остался в поле — отправьте его ещё раз.',
    }),
  } : {
    title: 'Вопрос не отправлен',
    text: publicApiErrorMessage(error, 'Вопрос сохранён — отправьте его ещё раз.', {
      forbidden: 'Нет прав задавать вопросы в этой саге.', network: unavailable, server: unavailable,
    }),
    action: 'ask_again' as const,
  });
  return supportCode ? { ...failure, supportCode } : failure;
}

function rateLimitText(seconds: number | null) {
  if (seconds === null) return 'Спросите снова чуть позже. Вопрос сохранён.';
  return seconds > 0
    ? `Спросить снова можно через ${formatRetryAfterSeconds(seconds)}. Вопрос сохранён.`
    : 'Можно спросить снова. Вопрос сохранён.';
}

/** A question that was not sent or a stop that failed; the 429 window counts down here. */
export function useResearchChatFailure(onAuthorityLost: () => void) {
  const [state, setState] = useState<{ failure: ChatFailure | null; retryDelaySeconds?: number | null }>({ failure: null });

  function clearPanelFailure() {
    setState({ failure: null });
  }

  function showPanelFailure(error: unknown, operation: 'start' | 'stop' | 'steer') {
    const statusFailure = isApiStatusFailure(error) ? error : null;
    if (statusFailure?.status === 403) onAuthorityLost();
    const supportCode = statusFailure?.problem.requestId || undefined;
    const quotaCode = quotaFailureCode(error);
    if (operation === 'stop') {
      setState({ failure: { text: 'Не удалось остановить ответ. Попробуйте ещё раз.', action: statusFailure?.status === 403 ? undefined : 'retry', supportCode } });
    } else if (quotaCode) {
      // A quota refusal is named from the account quota, with neither a countdown nor a retry (quota spec R5.4).
      setState({ failure: { text: publicApiErrorMessage(error, 'Вопрос не отправлен.'), quotaCode } });
    } else if (statusFailure?.status === 429) {
      setState({ failure: { title: operation === 'steer' ? 'Слишком много уточнений подряд' : 'Слишком много вопросов подряд', text: '', action: 'ask_again', supportCode }, retryDelaySeconds: retryAfterSeconds(error) });
    } else {
      const failure = sendFailure(error, supportCode, operation === 'steer');
      setState({ failure: statusFailure?.status === 403 ? { ...failure, action: undefined } : failure });
    }
  }

  useEffect(() => {
    if (typeof state.retryDelaySeconds !== 'number' || state.retryDelaySeconds <= 0) return undefined;
    const timer = window.setTimeout(() => {
      setState((current) => typeof current.retryDelaySeconds === 'number'
        ? { ...current, retryDelaySeconds: Math.max(0, current.retryDelaySeconds - 1) }
        : current);
    }, 1_000);
    return () => window.clearTimeout(timer);
  }, [state.retryDelaySeconds]);

  const failure = state.failure && state.retryDelaySeconds !== undefined
    ? { ...state.failure, text: rateLimitText(state.retryDelaySeconds) }
    : state.failure;
  return { clearPanelFailure, failure, retryDelaySeconds: state.retryDelaySeconds, showPanelFailure };
}

export function latestUserBody(events: readonly CanonicalAgentTimelineEvent[]) {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event?.kind === 'user') return event.text;
  }
  return undefined;
}

interface ConversationFailureInput {
  /** The refusal of the last start or stop. */
  panelFailure: ChatFailure | null;
  /** The text of a quota refusal from the account quota; '' without one. */
  quotaRefusal: string;
  selectedChatMissing: boolean;
  sessionError: unknown;
  sessionErrorRequestId: string;
  recoveredJob?: Pick<ChatJob, 'status' | 'error'> | null;
  jobIsActive: boolean;
  connection: string;
}

/**
 * The one failure block under the turn (R4.2.3): a refused question, the chat read, the failed
 * Job or the stream, in that order; a quota refusal keeps its block without a retry and names its
 * limit from the account quota (quota spec R5.3).
 */
export function conversationFailure(input: ConversationFailureInput) {
  const loadFailure: ChatFailure | null = input.selectedChatMissing
    ? { text: 'Чат не найден.' }
    : input.sessionError
      ? { text: publicApiErrorMessage(input.sessionError, 'Чат не найден.'), action: 'retry', supportCode: input.sessionErrorRequestId || undefined }
      : null;
  const sendFailure = input.panelFailure?.quotaCode ? { ...input.panelFailure, text: input.quotaRefusal || input.panelFailure.text } : input.panelFailure;
  const failure = sendFailure ?? loadFailure ?? (input.recoveredJob?.status === 'failed' ? turnFailure(input.recoveredJob.error) : null)
    ?? (input.jobIsActive && (input.connection === 'reconnecting' || input.connection === 'closed') ? streamFailure(input.connection === 'closed') : null);
  return { failure, loadFailure, sendFailure };
}
