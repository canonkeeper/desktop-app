import { mutationOptions, queryOptions, useMutation, useQuery, type QueryClient } from '@tanstack/react-query';
import {
  ApiStatusError,
  NetworkApiError,
  type ApiRequestOptions,
  type CanonKeeperApiClient,
  type OperationRequest,
  type OperationResponse,
} from '../../../shared/api';
import { publicApiErrorMessage } from '../../api-errors';

export type CurrentUser = OperationResponse<'getCurrentUser', '200'>;
export type AuthSession = OperationResponse<'loginUser', '200'>;
export type LoginRequest = OperationRequest<'loginUser'>;
export type RegisterRequest = OperationRequest<'registerUser'>;
export type AuthFailureKind = 'unauthorized' | 'forbidden' | 'rate_limited' | 'server' | 'network' | 'unknown';
export type SessionAuthClient = Pick<CanonKeeperApiClient, 'login' | 'register'>;
/** Account letters and their links (UC1–UC2). */
export type AccountMailClient = Pick<
  CanonKeeperApiClient,
  'sendEmailVerification' | 'confirmEmailVerification' | 'requestPasswordReset' | 'resetPassword'
>;
/** The signed-in writer's own sign-in settings (UC3). */
export type AccountSecurityClient = Pick<CanonKeeperApiClient, 'changePassword' | 'logoutEverywhere'>;
/** The acceptance of the current terms and personal data consent (legal R3.2). */
export type LegalDocumentsClient = Pick<CanonKeeperApiClient, 'acceptLegalDocuments'>;
export const emailUnverifiedMessage = 'Подтвердите адрес почты, чтобы загружать книги и задавать вопросы.';
export const mailUnavailableMessage = 'Не удалось отправить письмо. Попробуйте через несколько минут.';
export type SessionLogoutClient = Pick<CanonKeeperApiClient, 'logout'>;
type CurrentUserClient = Pick<CanonKeeperApiClient, 'getCurrentUser'>;

export interface AuthFailure {
  kind: AuthFailureKind;
  message: string;
}

export const canonKeeperQueryKeys = {
  all: ['canonkeeper'] as const,
};

export const sessionQueryKeys = {
  all: [...canonKeeperQueryKeys.all, 'session'] as const,
  currentUser: () => [...sessionQueryKeys.all, 'currentUser'] as const,
};

interface CurrentUserQueryMeta extends Record<string, unknown> {
  canonKeeperApi: CurrentUserClient;
}

function currentUserQueryMeta(api: CurrentUserClient): CurrentUserQueryMeta {
  return { canonKeeperApi: api };
}

function apiFromQueryMeta(meta: unknown) {
  const queryMeta = meta as Partial<CurrentUserQueryMeta> | undefined;
  if (!queryMeta?.canonKeeperApi) {
    throw new Error('Current user query is missing CanonKeeper API client metadata.');
  }
  return queryMeta.canonKeeperApi;
}

export function currentUserQueryOptions(api: CurrentUserClient) {
  return queryOptions({
    queryKey: sessionQueryKeys.currentUser(),
    queryFn: ({ meta, signal }) => apiFromQueryMeta(meta).getCurrentUser({ signal } satisfies ApiRequestOptions),
    meta: currentUserQueryMeta(api),
    // Проверка сессии должна работать и во время глобальной паузы остальных запросов.
    networkMode: 'always',
    retry: false,
    staleTime: 30 * 1000,
  });
}

export function logoutMutationOptions(api: SessionLogoutClient) {
  return mutationOptions({
    mutationFn: () => api.logout(),
  });
}

export function classifyAuthFailure(error: unknown, fallback: string): AuthFailure {
  if (error instanceof ApiStatusError) {
    if (error.status === 401) {
      if (error.problem.code === 'auth.invalid_credentials') {
        return { kind: 'unauthorized', message: 'Неверный email или пароль.' };
      }
      return { kind: 'unauthorized', message: 'Время входа истекло. Войдите снова — вернём вас на ту же страницу.' };
    }
    if (error.status === 403) {
      if (error.problem.code === 'auth.email_unverified') return { kind: 'forbidden', message: emailUnverifiedMessage };
      return { kind: 'forbidden', message: 'Нет доступа к этой странице.' };
    }
    if (error.status === 429) {
      return { kind: 'rate_limited', message: `Слишком много попыток. Попробуйте снова ${retryAfterText(error.response)}.` };
    }
    if (error.status >= 500) {
      if (error.problem.code === 'mail.unavailable') return { kind: 'server', message: mailUnavailableMessage };
      return { kind: 'server', message: 'Сервис временно недоступен. Попробуйте снова.' };
    }
    return { kind: 'unknown', message: publicApiErrorMessage(error, fallback) };
  }

  if (error instanceof NetworkApiError) {
    return { kind: 'network', message: 'Не удалось подключиться к серверу. Проверьте соединение и попробуйте снова.' };
  }

  return { kind: 'unknown', message: fallback };
}

/** Seconds from the Retry-After header of a 429, or null. */
export function retryAfterSeconds(error: unknown) {
  if (!(error instanceof ApiStatusError) || error.status !== 429) return null;
  const seconds = Number(error.response.headers.get('Retry-After'));
  return Number.isFinite(seconds) && seconds > 0 ? seconds : null;
}

/** True when the backend refused model work because the address is not verified. */
export function isEmailUnverifiedFailure(error: unknown) {
  return error instanceof ApiStatusError && error.status === 403 && error.problem.code === 'auth.email_unverified';
}

function retryAfterText(response: Response) {
  const seconds = Number(response.headers.get('Retry-After'));
  if (!Number.isFinite(seconds) || seconds <= 0) return 'чуть позже';
  const minutes = Math.ceil(seconds / 60);
  return minutes <= 1 ? 'через минуту' : `через ${minutes} мин`;
}

export function useCurrentUserQuery(api: CurrentUserClient) {
  return useQuery(currentUserQueryOptions(api));
}

export function useLogoutMutation(api: SessionLogoutClient) {
  return useMutation(logoutMutationOptions(api));
}

export function setSessionUser(queryClient: QueryClient, user: CurrentUser) {
  queryClient.setQueryData(sessionQueryKeys.currentUser(), user);
}

export function clearSessionUser(queryClient: QueryClient) {
  queryClient.setQueryData<CurrentUser | null>(sessionQueryKeys.currentUser(), null);
}

export function clearCanonKeeperQueryCache(queryClient: QueryClient) {
  queryClient.removeQueries({ queryKey: canonKeeperQueryKeys.all });
}

export function clearCanonKeeperProductCache(queryClient: QueryClient) {
  queryClient.removeQueries({
    queryKey: canonKeeperQueryKeys.all,
    predicate: (query) => query.queryKey[1] !== 'session',
  });
}
