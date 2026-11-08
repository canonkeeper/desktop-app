import { ApiStatusError, NetworkApiError } from '../shared/api';

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
  'auth.email_unverified': 'Подтвердите адрес почты, чтобы загружать книги и задавать вопросы.',
  'mail.unavailable': 'Не удалось отправить письмо. Попробуйте через несколько минут.',
};

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
    if (error.status >= 500) return messages.server ?? 'Сервис временно недоступен. Попробуйте снова.';
    return fallback;
  }

  if (isNetworkFailure(error)) {
    return messages.network ?? 'Не удалось подключиться к серверу. Проверьте соединение и попробуйте снова.';
  }

  return fallback;
}
