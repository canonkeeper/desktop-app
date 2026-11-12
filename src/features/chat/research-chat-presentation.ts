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
  /** A `quota.*` refusal of a message: the panel names the limit from the account quota in the block above the field. */
  quotaCode?: string;
  /**
   * The failure of the Job of the turn: what became of the question by the rules of the quota, one line
   * (specs/2026-11-10-ux1-chat.md CH3.6). Such a failure is the block under the question of its turn
   * (specs/2026-11-10-ux4-chat-waiting.md CW2.8).
   */
  count?: string;
}

/** A chat of a previous assistant version keeps its history but takes no new questions. */
export function outdatedChatMessage() {
  return 'Этот чат вёл прежний помощник: история сохранена, но продолжить его нельзя. Задайте вопрос в новом чате.';
}

// Data «Отказы хода»: the codes of a failed turn Job and of a refused question. The author knows the helper, not the
// models behind it (specs/2026-11-10-ux4-chat-waiting.md CW2.8; Ч-31).
const failuresByCode: Readonly<Record<string, ChatFailure>> = {
  // The shared limit of the service (specs/2026-11-11-ux9-shelf-chat.md SC3.2, Ч-32): when to come back and «Повторить вопрос»;
  // the count is said in its words.
  'model.usage_limit_exceeded': {
    title: 'Сервис перегружен',
    text: '',
    action: 'ask_again',
    count: 'Вопрос не засчитан — повторите через несколько минут.',
  },
  'model.server_overloaded': { title: 'Помощник сейчас перегружен', text: 'Подождите пару минут и спросите снова — вопрос сохранён.', action: 'ask_again' },
  'model.http_connection_failed': {
    title: 'Нет связи с помощником', text: 'Связь с помощником прервалась. Спросите снова через несколько минут.', action: 'ask_again',
  },
  'model.context_window_exceeded': {
    title: 'Разговор стал слишком длинным для помощника',
    text: 'Начните новый чат — книги и прежние ответы никуда не денутся.',
    action: 'new_chat',
  },
  'model.turn_interrupted': {
    title: 'Ответ прервался', text: 'Мы обновляли сервис, и ответ остановился. Спросите снова — вопрос сохранён.', action: 'ask_again',
  },
  start_indexing: { title: 'Помощник ещё не прочитал книгу', text: 'Откройте «Книги» и нажмите «Дочитать» у книги.', action: 'books' },
  'chat.outdated_agent': { text: outdatedChatMessage() },
  // A correction (specs/2026-10-30-chat-steering.md R4.3): the text stays in the field.
  'chat.turn_ended': { title: 'Помощник уже ответил', text: 'Текст остался в поле — отправьте его новым вопросом.', action: 'ask_again' },
  'chat.steer_retry': { text: 'Сейчас уточнение не принимается. Текст остался в поле — повторите через несколько секунд.' },
  'chat.steer_limit': { text: 'Пока помощник отвечает, уточнений уже слишком много. Дождитесь ответа — текст останется в поле.' },
};

const unansweredTurn: ChatFailure = {
  title: 'Помощник не смог ответить',
  text: 'Спросите снова. Если ошибка повторится, напишите нам: код ошибки приложим сами.',
  action: 'ask_again',
};

/**
 * UC4.2: the block under a turn whose Job failed, by the code of the Job (R4.2.3, R4.2.4). The server gave the unit of
 * a failed turn back (specs/2026-11-09-quota-accounting.md Q2.4), and the block says so (specs/2026-11-10-ux1-chat.md CH3.6).
 */
export function turnFailure(error: ChatJob['error']): ChatFailure {
  const known = (error?.code && failuresByCode[error.code]) || unansweredTurn;
  const failure = { ...known, count: known.count ?? 'Этот вопрос не засчитан.' };
  return error?.requestId ? { ...failure, supportCode: error.requestId } : failure;
}

/** How long a lost stream is a quiet line before it is a block with «Написать нам» (AA2.6, Ч-74). */
export const streamLostBlockMs = 60_000;

/**
 * The stream of the feed has been lost for a minute (specs/2026-11-11-ux4-after-answer.md AA2.6): a short break is a
 * quiet line under the status of the turn, only a break that did not mend itself in a minute is a block.
 */
export function useStreamLostLong(connection: string) {
  const lost = connection === 'reconnecting' || connection === 'closed';
  const [long, setLong] = useState(false);
  useEffect(() => {
    setLong(false);
    if (!lost) return undefined;
    const timer = window.setTimeout(() => setLong(true), streamLostBlockMs);
    return () => window.clearTimeout(timer);
  }, [lost]);
  return lost && long;
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
  // No network is not a failure of the service: the words say which it is (AA2.8, Ч-34).
  const offline = 'Нет связи с сервером. Вопрос остался в поле.';
  const failure = known ?? (steering ? {
    title: 'Уточнение не отправлено',
    text: publicApiErrorMessage(error, 'Текст остался в поле — отправьте его ещё раз.', {
      forbidden: 'Нет прав задавать вопросы в этой саге.',
      network: 'Нет связи с сервером. Текст остался в поле.',
      server: 'Сервис временно недоступен. Текст остался в поле — отправьте его ещё раз.',
    }),
  } : {
    title: 'Вопрос не отправлен',
    text: publicApiErrorMessage(error, 'Вопрос сохранён — отправьте его ещё раз.', {
      forbidden: 'Нет прав задавать вопросы в этой саге.', network: offline, server: unavailable,
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
  /** The refusal met a limit the block above the field names: the feed shows no second block (CH3.3). */
  limitNamed: boolean;
  selectedChatMissing: boolean;
  sessionError: unknown;
  sessionErrorRequestId: string;
  recoveredJob?: Pick<ChatJob, 'status' | 'error'> | null;
  jobIsActive: boolean;
  connection: string;
}

/**
 * The one failure block under the turn (R4.2.3): a refused question, the chat read, the failed
 * Job or the stream, in that order. A refusal by the questions of the day or by the answers being
 * prepared is not here: it is the block above the field (specs/2026-11-10-ux1-chat.md CH3.3); any
 * other quota refusal keeps its block without a retry (quota spec R5.4).
 */
export function conversationFailure(input: ConversationFailureInput) {
  const loadFailure: ChatFailure | null = input.selectedChatMissing
    ? { text: 'Чат не найден.' }
    : input.sessionError
      ? { text: publicApiErrorMessage(input.sessionError, 'Чат не найден.'), action: 'retry', supportCode: input.sessionErrorRequestId || undefined }
      : null;
  const sendFailure = input.panelFailure?.quotaCode && input.limitNamed ? null : input.panelFailure;
  const failure = sendFailure ?? loadFailure ?? (input.recoveredJob?.status === 'failed' ? turnFailure(input.recoveredJob.error) : null)
    ?? (input.jobIsActive && (input.connection === 'reconnecting' || input.connection === 'closed') ? streamFailure(input.connection === 'closed') : null);
  return { failure, loadFailure, sendFailure };
}
