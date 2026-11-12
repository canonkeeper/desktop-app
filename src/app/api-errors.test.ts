import { describe, expect, it } from 'vitest';
import { ApiStatusError, NetworkApiError, type ApiProblem } from '../shared/api';
import { classifyAuthFailure } from '../entities/session/api';

function statusError(status: number, detail?: string, code = `status_${status}`) {
  const problem: ApiProblem = {
    type: 'about:blank',
    title: `Status ${status}`,
    status,
    detail,
    code,
    requestId: 'req_test',
  };
  return new ApiStatusError(status, problem, new Response(null, { status }));
}

describe('classifyAuthFailure', () => {
  it('keeps auth failures distinguishable by status and network shape', () => {
    expect(classifyAuthFailure(statusError(401, 'invalid credentials', 'auth.invalid_credentials'), 'fallback')).toEqual({
      kind: 'unauthorized',
      message: 'Проверьте почту и пароль.',
    });
    expect(classifyAuthFailure(statusError(401, 'authentication required', 'auth.session_required'), 'fallback')).toEqual({
      kind: 'unauthorized',
      message: 'Время входа истекло. Войдите снова — вернём вас на ту же страницу.',
    });
    expect(classifyAuthFailure(statusError(403), 'fallback')).toMatchObject({ kind: 'forbidden' });
    expect(classifyAuthFailure(statusError(500), 'fallback')).toMatchObject({ kind: 'server' });
    expect(classifyAuthFailure(new NetworkApiError(new Error('down')), 'fallback')).toMatchObject({ kind: 'network' });
    expect(classifyAuthFailure(statusError(418, 'teapot'), 'fallback')).toMatchObject({
      kind: 'unknown',
      message: 'fallback',
    });
  });

  it('tells how long to wait after too many attempts', () => {
    const throttled = (retryAfter: string | null) => new ApiStatusError(
      429,
      { type: 'about:blank', title: 'Too Many Requests', status: 429, code: 'rate_limit.exceeded', requestId: 'req_test' },
      new Response(null, { status: 429, headers: retryAfter ? { 'Retry-After': retryAfter } : {} }),
    );
    expect(classifyAuthFailure(throttled('600'), 'fallback')).toEqual({
      kind: 'rate_limited',
      message: 'Слишком много попыток. Попробуйте снова через 10 мин.',
    });
    expect(classifyAuthFailure(throttled('20'), 'fallback')).toMatchObject({ message: 'Слишком много попыток. Попробуйте снова через минуту.' });
    expect(classifyAuthFailure(throttled(null), 'fallback')).toMatchObject({ message: 'Слишком много попыток. Попробуйте снова чуть позже.' });
  });
});

// specs/2026-11-10-ux2-patterns-refusals.md PR3.3: the cause of an error in the words of the author.
describe('loadFailureWords', () => {
  it('names no connection, a busy service and a failure on our side without technical words', async () => {
    const { loadFailureWords, publicApiErrorMessage } = await import('../entities/api-errors');
    expect(loadFailureWords(new NetworkApiError(new Error('down')))).toEqual({ cause: 'Нет связи с сервером. Проверьте интернет.', code: null });
    expect(loadFailureWords(statusError(503))).toEqual({ cause: 'Сервис сейчас перегружен, попробуйте через минуту.', code: 'req_test' });
    expect(loadFailureWords(statusError(500))).toEqual({ cause: 'Это сбой на нашей стороне, попробуйте через минуту.', code: 'req_test' });
    expect(loadFailureWords(statusError(403)).cause).toBe('У вас нет доступа к этой саге.');
    expect(publicApiErrorMessage(statusError(502), 'fallback')).toBe('Сервис сейчас перегружен, попробуйте через минуту.');
    for (const status of [401, 403, 404, 429, 500, 502, 503]) {
      expect(loadFailureWords(statusError(status)).cause).not.toMatch(/ошибка сервера|запрос|статус|server|status/iu);
    }
  });
});
