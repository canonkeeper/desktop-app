import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../api/mock/repository';
import { createApiHandlers, mswApiBaseUrl } from './handlers';
import { mswServer } from './server';

const headers = {
  cookie: 'ck_session=mock-session',
  origin: 'http://localhost:3000',
  'x-csrf-token': 'ck_msw_csrf_token_000000',
  'content-type': 'application/json',
};

describe('Notes V2 MSW routes', () => {
  it('reads summaries through a protected JSON query, and fetches full detail separately', async () => {
    const repository = createMockApiClient();
    const note = await repository.createPersonalNote({ title: 'Личная %_ запись', body: 'Полный текст для detail.' }, { idempotencyKey: 'idem-msw-note-summary' });
    mswServer.use(...createApiHandlers({ repository }));
    const response = await fetch(`${mswApiBaseUrl}/notes/query`, { method: 'POST', headers, body: JSON.stringify({ query: '  личная %_ ', source: 'free' }) });
    expect(response.status).toBe(200);
    expect(response.headers.get('cache-control')).toBe('no-store');
    const result = await response.json();
    expect(result).toMatchObject({ data: [{ id: note.id, sourceKind: 'free', title: note.title, preview: note.body }], meta: { totalCount: 1, hasMore: false, nextCursor: null } });
    expect(result.data[0]).not.toHaveProperty('body');
    expect(result.data[0]).not.toHaveProperty('source');
    expect(result.data[0]).not.toHaveProperty('kind');
    const detail = await fetch(`${mswApiBaseUrl}/personal-notes/${note.id}`, { headers });
    await expect(detail.json()).resolves.toEqual(note);
  });

  it('requires session and CSRF and never accepts search text in URL', async () => {
    mswServer.use(...createApiHandlers());
    const url = `${mswApiBaseUrl}/notes/query`;
    const anonymousHeaders = new Headers(headers);
    anonymousHeaders.delete('cookie');
    const anonymous = await fetch(url, { method: 'POST', headers: anonymousHeaders, body: '{}' });
    expect(anonymous.status).toBe(401);
    const noCsrf = await fetch(url, { method: 'POST', headers: { cookie: headers.cookie, origin: headers.origin, 'content-type': 'application/json' }, body: '{}' });
    expect(noCsrf.status).toBe(403);
    const inUrl = await fetch(`${url}?query=private`, { method: 'POST', headers, body: '{}' });
    expect(inUrl.status).toBe(400);
    const invalid = await fetch(url, { method: 'POST', headers, body: 'null' });
    expect(invalid.status).toBe(400);
    const malformed = await fetch(url, { method: 'POST', headers, body: '{' });
    expect(malformed.status).toBe(400);
  });

  it('supports title-only PATCH without replacing note body', async () => {
    const repository = createMockApiClient();
    const note = await repository.createPersonalNote({ body: 'Сохранить тело' }, { idempotencyKey: 'idem-msw-note-title' });
    mswServer.use(...createApiHandlers({ repository }));
    const response = await fetch(`${mswApiBaseUrl}/personal-notes/${note.id}`, { method: 'PATCH', headers, body: JSON.stringify({ title: 'Название', expectedRevision: 1 }) });
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toMatchObject({ title: 'Название', body: note.body, revision: 2 });
  });
});
