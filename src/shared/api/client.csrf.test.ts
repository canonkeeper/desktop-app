import { describe, expect, it } from 'vitest';
import { createCanonKeeperApiClient } from './index';

const csrfValues = ['csrf_test_token_12345', 'ck_csrf_example_token_000000'] as const;

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('OpenApiCanonKeeperClient CSRF recovery', () => {
  it('asks for one token for writes sent together: another answer would void the first token', async () => {
    let csrfVersion = 0;
    let valid = '';
    const statuses: number[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        if (request.url.endsWith('/auth/csrf')) {
          valid = csrfValues[csrfVersion] ?? '';
          csrfVersion += 1;
          await new Promise((resolve) => setTimeout(resolve, 1));
          return jsonResponse({ csrfToken: valid, expiresAt: '2026-09-01T20:00:00.000Z', rotation: 'refreshed' });
        }
        const accepted = request.headers.get('X-CSRF-Token') === valid;
        statuses.push(accepted ? 201 : 403);
        return accepted
          ? jsonResponse({ job: { id: 'job_1' } }, 202)
          : jsonResponse({ type: 'about:blank', title: 'Forbidden', status: 403, requestId: 'req_csrf' }, 403);
      },
    });

    await Promise.all([
      api.createChatTurn('chat_1', { body: 'Первый' }, { idempotencyKey: 'idem-csrf-together-1' }),
      api.createChatTurn('chat_2', { body: 'Второй' }, { idempotencyKey: 'idem-csrf-together-2' }),
    ]);

    expect(csrfVersion).toBe(1);
    expect(statuses).toEqual([201, 201]);
  });

  it('reacquires CSRF and retries once after the server rejects a stale token', async () => {
    const calls: Request[] = [];
    let csrfVersion = 0;
    let applyAttempt = 0;
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          csrfVersion += 1;
          return jsonResponse({
            csrfToken: csrfValues[csrfVersion - 1],
            expiresAt: '2026-09-01T20:00:00.000Z',
            rotation: 'refreshed',
          });
        }
        applyAttempt += 1;
        if (applyAttempt === 1) {
          return jsonResponse({
            type: 'about:blank',
            title: 'Forbidden',
            status: 403,
            code: 'csrf.invalid',
            requestId: 'req_stale_csrf',
          }, 403);
        }
        return jsonResponse({ job: { id: 'job_1' } }, 202);
      },
    });

    await expect(api.createChatTurn('chat_1', { body: 'Вопрос' }, { idempotencyKey: 'idem-stale-csrf' }))
      .resolves.toMatchObject({ job: { id: 'job_1' } });

    expect(calls.map((request) => new URL(request.url).pathname)).toEqual([
      '/api/v1/auth/csrf',
      '/api/v1/chats/chat_1/turns',
      '/api/v1/auth/csrf',
      '/api/v1/chats/chat_1/turns',
    ]);
    expect(calls[1]?.headers.get('X-CSRF-Token')).toBe(csrfValues[0]);
    expect(calls[3]?.headers.get('X-CSRF-Token')).toBe(csrfValues[1]);
    expect(calls[1]?.headers.get('Idempotency-Key')).toBe('idem-stale-csrf');
    expect(calls[3]?.headers.get('Idempotency-Key')).toBe('idem-stale-csrf');
  });
});
