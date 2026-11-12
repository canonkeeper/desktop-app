import { ApiStatusError, NetworkApiError } from '../shared/api';
import { emailUnverifiedShort } from '../shared/session/account-session';

export interface ApiStatusFailure {
  problem: {
    code?: string;
    detail?: string;
    requestId?: string;
    title?: string;
  };
  status: number;
}

export interface NetworkFailure {
  cause: unknown;
}

export interface PublicApiErrorMessages {
  codes?: Record<string, string>;
  conflict?: string;
  forbidden?: string;
  network?: string;
  server?: string;
  unauthorized?: string;
}

export function isApiStatusFailure(error: unknown): error is ApiStatusFailure {
  return error instanceof ApiStatusError;
}

export function isNetworkFailure(error: unknown): error is NetworkFailure {
  return error instanceof NetworkApiError;
}

export function isTransientApiFailure(error: unknown) {
  return isNetworkFailure(error) || (isApiStatusFailure(error) && error.status >= 500);
}

// Account codes read the same on every surface (UC1.4, UC4).
const accountCodeMessages: Record<string, string> = {
  'auth.email_unverified': emailUnverifiedShort,
  'mail.unavailable': 'Не удалось отправить письмо. Попробуйте через несколько минут.',
  // The demo saga of the site takes no text of the author (specs/2026-11-09-demo-saga.md DS3.3).
  'demo_saga.read_only': 'Это сага-пример. Создайте свою сагу — книги загружаются в неё',
};

/**
 * Why a request failed, in the words of the author (specs/2026-11-10-ux2-patterns-refusals.md PR3.3): no connection, the
 * service is busy, a failure on our side. «Ошибка сервера», «запрос», «статус» are not his words.
 */
const noConnection = 'Нет связи с сервером. Проверьте интернет.';
const busy = 'Сервис сейчас перегружен, попробуйте через минуту.';
const ourSide = 'Это сбой на нашей стороне, попробуйте через минуту.';
const busyStatuses = new Set([429, 502, 503, 504]);

function serverWords(status: number) {
  return busyStatuses.has(status) ? busy : ourSide;
}

/**
 * What the one block of an error of a section says of its cause and the code support finds it by (PR3.1–PR3.3, PR3.5):
 * the section names what did not open, this says why.
 */
export function loadFailureWords(error: unknown): { cause: string; code: string | null } {
  if (isNetworkFailure(error)) return { cause: noConnection, code: null };
  if (!isApiStatusFailure(error)) return { cause: '', code: null };
  const code = error.problem.requestId ?? null;
  if (error.status === 401) return { cause: 'Время входа истекло — войдите снова.', code };
  if (error.status === 403) return { cause: 'У вас нет доступа к этой саге.', code };
  if (error.status === 404) return { cause: 'Её нет: она удалена или ссылка устарела.', code };
  if (busyStatuses.has(error.status) || error.status >= 500) return { cause: serverWords(error.status), code };
  return { cause: '', code };
}

export function publicApiErrorMessage(error: unknown, fallback: string, messages: PublicApiErrorMessages = {}) {
  if (isApiStatusFailure(error)) {
    if (error.problem.code && messages.codes && Object.hasOwn(messages.codes, error.problem.code)) {
      return messages.codes[error.problem.code] ?? fallback;
    }
    if (error.problem.code && Object.hasOwn(accountCodeMessages, error.problem.code)) {
      return accountCodeMessages[error.problem.code]!;
    }
    if (error.status === 401) return messages.unauthorized ?? fallback;
    if (error.status === 403) return messages.forbidden ?? fallback;
    if (error.status === 409) return messages.conflict ?? fallback;
    if (error.status >= 500) return messages.server ?? serverWords(error.status);
    return fallback;
  }

  if (isNetworkFailure(error)) {
    return messages.network ?? noConnection;
  }

  return fallback;
}
