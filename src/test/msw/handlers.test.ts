import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../api/mock/repository';
import { mockPromoAnswerLink, mockPromoRevokeLink } from '../../api/mock/repository/promo-permissions';
import { mockReadingTestSlug } from '../../api/mock/repository/reading-test';
import { mockAccountToken } from '../../api/mock/repository/session';
import { getOperationManifestEntries, type OperationId } from '../../shared/api';
import { createApiHandlers, createAuthHandlers, mswApiBaseUrl } from './handlers';
import { mswServer } from './server';

const pathParams: Record<string, string> = {
  artifactId: 'artifact-reader-references-1',
  bookId: 'book-02',
  chapterId: 'chapter-16',
  invitationId: 'invitation-mira',
  jobId: 'canon-text-index-job-1',
  memberId: 'member-collaborator',
  number: '1',
  permissionId: 'promo-white-port-writer',
  projectId: 'project-white-port',
  queueId: 'import-queue-1',
  requestId: 'illustration-request-1',
  userId: '11111111-0000-4000-8000-000000000002',
};

async function mswCsrfToken() {
  const response = await fetch(`${mswApiBaseUrl}/auth/csrf`);
  expect(response.status).toBe(200);
  const body = (await response.json()) as { csrfToken: string };
  return body.csrfToken;
}

function expectSessionCookieAttributes(setCookie: string) {
  expect(setCookie).toContain('ck_session=mock-session');
  expect(setCookie).toContain('Path=/');
  expect(setCookie).toContain('HttpOnly');
  expect(setCookie).toContain('SameSite=Lax');
  expect(setCookie).toContain('Max-Age=43200');
}

function rawMultipartImportBody(boundary: string, fileBody: Uint8Array) {
  const encoder = new TextEncoder();
  const chunks = [
    encoder.encode(
      [
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="white-port.epub"',
        'Content-Type: application/epub+zip',
        '',
      ].join('\r\n') + '\r\n',
    ),
    fileBody,
    encoder.encode(
      '\r\n' + [
        `--${boundary}`,
        'Content-Disposition: form-data; name="metadata"',
        'Content-Type: application/json',
        '',
        JSON.stringify({ sourceFileName: 'white-port.epub', title: 'White Port Append' }),
        `--${boundary}`,
        'Content-Disposition: form-data; name="options"',
        'Content-Type: application/json',
        '',
        JSON.stringify({ importMode: 'new_book' }),
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    ),
  ];
  const byteLength = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
  const body = new Uint8Array(byteLength);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return body;
}

describe('MSW API handlers', () => {
  it('has an explicit handler for every generated operation route', async () => {
    for (const [operationId, route] of getOperationManifestEntries()) {
      // The WebSockets of the timeline and of a voice call are routed by the tests that open them.
      if (operationId === 'streamAgentTimeline' || operationId === 'streamVoiceCall') continue;
      const repository = createMockApiClient();
      const operationPathParams = await prepareRepositoryForOperation(repository, operationId);
      mswServer.resetHandlers(
        ...createAuthHandlers(operationId === 'sendEmailVerification' ? { emailVerified: false } : {}),
        ...createApiHandlers({ repository }),
      );
      const path = routePathForOperation(operationId, route.path, operationPathParams);
      const response = await fetchWithOperationTimeout(
        `${mswApiBaseUrl}${path}`,
        requestInit(operationId, route.method),
        operationId,
      );

      await expectGeneratedOperationResponse(operationId, response);
    }
  }, 15000);

  it('enforces session, CSRF, strict local Origin, roles and no-store in the MSW harness', async () => {
    mswServer.use(...createApiHandlers());

    const missingSession = await fetch(`${mswApiBaseUrl}/projects/project-white-port`);
    expect(missingSession.status).toBe(401);
    expect(missingSession.headers.get('cache-control')).toBe('no-store');
    expect(missingSession.headers.get('content-type')).toContain('application/problem+json');

    const missingCsrf = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
      },
      body: JSON.stringify({ title: 'No CSRF' }),
    });
    expect(missingCsrf.status).toBe(403);

    const crossSiteSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'https://example.invalid',
      },
      body: JSON.stringify({ query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(crossSiteSearch.status).toBe(403);
    const missingOriginSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
      },
      body: JSON.stringify({ query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(missingOriginSearch.status).toBe(403);
    const missingSearchCsrf = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
      },
      body: JSON.stringify({ query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(missingSearchCsrf.status).toBe(403);

    const viewerMutation = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        mock_actor_role: 'viewer',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ title: 'Viewer blocked' }),
    });
    expect(viewerMutation.status).toBe(403);

    const viewerMissingProjectMutation = await fetch(`${mswApiBaseUrl}/projects/project-missing`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        mock_actor_role: 'viewer',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ title: 'Missing project stays hidden' }),
    });
    expect(viewerMissingProjectMutation.status).toBe(404);

    const invalidProjectBody = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ id: 'project-hijack', title: 'Нельзя менять id' }),
    });
    expect(invalidProjectBody.status).toBe(400);

    const editorDeleteProject = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'DELETE',
      headers: {
        cookie: 'ck_session=mock-session',
        mock_actor_role: 'editor',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
    });
    expect(editorDeleteProject.status).toBe(403);

    const ownerDemotion = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-owner`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ role: 'viewer' }),
    });
    expect(ownerDemotion.status).toBe(409);

    const ownerRemoval = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-owner`, {
      method: 'DELETE',
      headers: {
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
    });
    expect(ownerRemoval.status).toBe(409);

    const invalidRoleUpdate = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-collaborator`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ role: 'owner' }),
    });
    expect(invalidRoleUpdate.status).toBe(400);

    const collaboratorUpdate = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-collaborator`, {
      method: 'PATCH',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
      body: JSON.stringify({ role: 'viewer' }),
    });
    expect(collaboratorUpdate.status).toBe(200);

    const collaboratorRemoval = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-collaborator`, {
      method: 'DELETE',
      headers: {
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
      },
    });
    expect(collaboratorRemoval.status).toBe(204);

    const ownerProjects = await fetch(`${mswApiBaseUrl}/projects`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(ownerProjects.status).toBe(200);
    expect(ownerProjects.headers.get('cache-control')).toBe('no-store');

    const firstBooksPage = await fetch(`${mswApiBaseUrl}/projects/project-white-port/books?limit=1`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    const firstBooksBody = (await firstBooksPage.json()) as { data: Array<{ id: string }>; meta: { nextCursor: string | null } };
    expect(firstBooksPage.status).toBe(200);
    expect(firstBooksBody.meta.nextCursor).toBeTruthy();
    const secondBooksPage = await fetch(`${mswApiBaseUrl}/projects/project-white-port/books?limit=1&cursor=${encodeURIComponent(firstBooksBody.meta.nextCursor ?? '')}`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    const secondBooksBody = (await secondBooksPage.json()) as { data: Array<{ id: string }> };
    expect(secondBooksPage.status).toBe(200);
    expect(secondBooksBody.data[0]?.id).not.toBe(firstBooksBody.data[0]?.id);

    const invalidChapterCursor = await fetch(`${mswApiBaseUrl}/books/book-02/chapters?limit=1&cursor=bad-cursor`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(invalidChapterCursor.status).toBe(400);

    // The active flag of the job list is the literal true or false, as the backend reads it.
    const looseActiveFlag = await fetch(`${mswApiBaseUrl}/projects/project-white-port/jobs?active=1`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(looseActiveFlag.status).toBe(400);

    const crossSiteProtectedGet = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      headers: { cookie: 'ck_session=mock-session', origin: 'https://example.invalid' },
    });
    expect(crossSiteProtectedGet.status).toBe(200);
    expect(crossSiteProtectedGet.headers.get('access-control-allow-origin')).toBeNull();

  });

  it('returns contract 415 Problem for non-multipart MSW imports', async () => {
    mswServer.use(...createApiHandlers());
    const response = await fetch(`${mswApiBaseUrl}/projects/project-white-port/imports`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
        'idempotency-key': 'idem-msw-import-json-body',
      },
      body: JSON.stringify({ file: 'not-multipart' }),
    });
    expect(response.status).toBe(415);
    expect(response.headers.get('content-type')).toContain('application/problem+json');
    const problem = (await response.json()) as { code: string; status: number };
    expect(problem).toMatchObject({ code: 'unsupported_media_type', status: 415 });
  });

  it('keeps logout replay bound to the retained session CSRF token', async () => {
    mswServer.use(...createAuthHandlers());
    const bootstrapToken = await mswCsrfToken();
    const login = await fetch(`${mswApiBaseUrl}/auth/login`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        origin: 'http://localhost:3000',
        'x-csrf-token': bootstrapToken,
      },
      body: JSON.stringify({ email: 'mira@example.com', password: 'white-port-12', rememberMe: true }),
    });
    expect(login.status).toBe(200);
    const loginSetCookie = login.headers.get('set-cookie') ?? '';
    expectSessionCookieAttributes(loginSetCookie);
    const loginCookie = loginSetCookie.split(';')[0] ?? '';
    const sessionWithLoginCookie = await fetch(`${mswApiBaseUrl}/auth/session`, {
      headers: { cookie: loginCookie },
    });
    expect(sessionWithLoginCookie.status).toBe(200);
    const sessionWithLoginCookieBody = (await sessionWithLoginCookie.json()) as Record<string, unknown>;
    expect(sessionWithLoginCookieBody).toMatchObject({ email: 'mira@example.com' });
    expect(sessionWithLoginCookieBody).not.toHaveProperty('user');

    const staleAfterLogin = await fetch(`${mswApiBaseUrl}/auth/session/rotate`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': bootstrapToken },
    });
    expect(staleAfterLogin.status).toBe(403);

    const postLoginToken = await mswCsrfToken();
    const rotate = await fetch(`${mswApiBaseUrl}/auth/session/rotate`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postLoginToken },
    });
    expect(rotate.status).toBe(200);
    expectSessionCookieAttributes(rotate.headers.get('set-cookie') ?? '');
    await expect(rotate.json()).resolves.toMatchObject({ user: { email: 'mira@example.com' } });

    const staleAfterRotate = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postLoginToken },
    });
    expect(staleAfterRotate.status).toBe(403);

    const postRotateToken = await mswCsrfToken();
    const logoutWithoutCookie = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { origin: 'http://localhost:3000' },
    });
    expect(logoutWithoutCookie.status).toBe(204);
    const logoutWithUnknownCookie = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=session-test', origin: 'http://localhost:3000' },
    });
    expect(logoutWithUnknownCookie.status).toBe(204);
    const sessionAfterNoopLogout = await fetch(`${mswApiBaseUrl}/auth/session`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(sessionAfterNoopLogout.status).toBe(200);
    const logout = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postRotateToken },
    });
    expect(logout.status).toBe(204);
    expect(logout.headers.get('set-cookie')).toContain('Max-Age=0');

    const replay = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postRotateToken },
    });
    expect(replay.status).toBe(204);

    const postLogoutToken = await mswCsrfToken();
    expect(postLogoutToken).toBe(postRotateToken);
    const staleSessionRead = await fetch(`${mswApiBaseUrl}/auth/session`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(staleSessionRead.status).toBe(401);
    const staleSessionRotate = await fetch(`${mswApiBaseUrl}/auth/session/rotate`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postLogoutToken },
    });
    expect(staleSessionRotate.status).toBe(401);
  });

  it('rejects stale MSW repository sessions after logout on protected routes', async () => {
    mswServer.use(...createApiHandlers());
    const csrfToken = await mswCsrfToken();
    const logout = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': csrfToken },
    });
    expect(logout.status).toBe(204);

    const postLogoutToken = await mswCsrfToken();
    const projects = await fetch(`${mswApiBaseUrl}/projects`, {
      headers: { cookie: 'ck_session=mock-session' },
    });
    expect(projects.status).toBe(401);
    const createBook = await fetch(`${mswApiBaseUrl}/projects/project-white-port/books`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': postLogoutToken,
      },
      body: JSON.stringify({ title: 'После выхода' }),
    });
    expect(createBook.status).toBe(401);
  });

  it('runs MSW security checks before protected body parsing', async () => {
    mswServer.use(...createApiHandlers());
    const unauthenticatedImport = await fetch(`${mswApiBaseUrl}/projects/project-white-port/imports`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        origin: 'http://localhost:3000',
        'idempotency-key': 'idem-msw-import-no-session',
      },
      body: JSON.stringify({ file: 'not-multipart' }),
    });
    expect(unauthenticatedImport.status).toBe(401);

    const missingImportCsrf = await fetch(`${mswApiBaseUrl}/projects/project-white-port/imports`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'idempotency-key': 'idem-msw-import-no-csrf',
      },
      body: JSON.stringify({ file: 'not-multipart' }),
    });
    expect(missingImportCsrf.status).toBe(403);

    const unauthenticatedSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        origin: 'http://localhost:3000',
      },
      body: 'not-json',
    });
    expect(unauthenticatedSearch.status).toBe(401);

    const missingBookCsrf = await fetch(`${mswApiBaseUrl}/projects/project-white-port/books`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
      },
      body: 'not-json',
    });
    expect(missingBookCsrf.status).toBe(403);
  });

  it('keeps MSW auth failures non-enumerating with stable problem codes', async () => {
    mswServer.use(...createAuthHandlers());
    const headers = {
      'content-type': 'application/json',
      origin: 'http://localhost:3000',
      'x-csrf-token': 'ck_msw_csrf_token_000000',
    };

    const unknownEmail = await fetch(`${mswApiBaseUrl}/auth/login`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ email: 'unknown@example.test', password: 'wrong-password', rememberMe: true }),
    });
    const wrongPassword = await fetch(`${mswApiBaseUrl}/auth/login`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ email: 'mira@example.com', password: 'wrong-password', rememberMe: true }),
    });
    const unknownEmailBody = (await unknownEmail.json()) as Record<string, unknown>;
    const wrongPasswordBody = (await wrongPassword.json()) as Record<string, unknown>;
    expect(unknownEmail.status).toBe(401);
    expect(wrongPassword.status).toBe(401);
    expect(unknownEmailBody).toMatchObject({ code: 'auth.invalid_credentials', detail: 'invalid credentials', status: 401, title: 'Unauthorized' });
    expect(wrongPasswordBody).toMatchObject({ code: 'auth.invalid_credentials', detail: 'invalid credentials', status: 401, title: 'Unauthorized' });
    expect(JSON.stringify(unknownEmailBody)).not.toContain('unknown@example.test');
    expect(JSON.stringify(wrongPasswordBody)).not.toContain('mira@example.com');

    const falseTermsRegister = await fetch(`${mswApiBaseUrl}/auth/register`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ acceptedTerms: false, displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' }),
    });
    expect(falseTermsRegister.status).toBe(400);
    expect(falseTermsRegister.headers.get('set-cookie')).toBeNull();
    const missingTermsRegister = await fetch(`${mswApiBaseUrl}/auth/register`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' }),
    });
    expect(missingTermsRegister.status).toBe(400);
    expect(missingTermsRegister.headers.get('set-cookie')).toBeNull();

    const existingEmailRegister = await fetch(`${mswApiBaseUrl}/auth/register`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ acceptedTerms: true, acceptedPersonalDataConsent: true, displayName: 'Мира Волкова', email: 'mira@example.com', password: 'white-port-12' }),
    });
    const postExistingRegisterToken = await mswCsrfToken();
    const newEmailRegister = await fetch(`${mswApiBaseUrl}/auth/register`, {
      method: 'POST',
      headers: { ...headers, 'x-csrf-token': postExistingRegisterToken },
      body: JSON.stringify({ acceptedTerms: true, acceptedPersonalDataConsent: true, displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' }),
    });
    // UC1.1: a taken address is 409 without a session; a new one signs in unverified.
    expect(existingEmailRegister.status).toBe(409);
    expect(existingEmailRegister.headers.get('set-cookie')).toBeNull();
    await expect(existingEmailRegister.json()).resolves.toMatchObject({ code: 'auth.email_taken' });
    expect(newEmailRegister.status).toBe(201);
    expectSessionCookieAttributes(newEmailRegister.headers.get('set-cookie') ?? '');
    await expect(newEmailRegister.json()).resolves.toMatchObject({ user: { email: 'new-author@example.test', emailVerified: false } });
  });

  it('rotates shared CSRF state through API handler auth endpoints', async () => {
    mswServer.use(...createApiHandlers());
    const bootstrapToken = await mswCsrfToken();
    const rotate = await fetch(`${mswApiBaseUrl}/auth/session/rotate`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': bootstrapToken },
    });
    expect(rotate.status).toBe(200);
    expectSessionCookieAttributes(rotate.headers.get('set-cookie') ?? '');
    await expect(rotate.json()).resolves.toMatchObject({ user: { email: 'mira@example.com' } });

    const staleUpdate = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json', cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': bootstrapToken },
      body: JSON.stringify({ title: 'Старый токен' }),
    });
    expect(staleUpdate.status).toBe(403);

    const postRotateToken = await mswCsrfToken();
    const freshUpdate = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json', cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postRotateToken },
      body: JSON.stringify({ title: 'Свежий токен' }),
    });
    expect(freshUpdate.status).toBe(200);

    const logout = await fetch(`${mswApiBaseUrl}/auth/logout`, {
      method: 'POST',
      headers: { cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postRotateToken },
    });
    expect(logout.status).toBe(204);

    const staleAfterLogout = await fetch(`${mswApiBaseUrl}/projects/project-white-port`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json', cookie: 'ck_session=mock-session', origin: 'http://localhost:3000', 'x-csrf-token': postRotateToken },
      body: JSON.stringify({ title: 'После выхода' }),
    });
    expect(staleAfterLogout.status).toBe(401);
  });

  it('binds MSW search cursors and invitation acceptance to request/security state', async () => {
    mswServer.use(...createApiHandlers());
    const headers = {
      'content-type': 'application/json',
      cookie: 'ck_session=mock-session',
      origin: 'http://localhost:3000',
      'x-csrf-token': 'ck_msw_csrf_token_000000',
    };
    const firstSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ limit: 1, query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    const firstBody = (await firstSearch.json()) as { meta: { nextCursor: string | null } };
    expect(firstSearch.status).toBe(200);
    expect(firstBody.meta.nextCursor).toBeNull();
    const mismatchSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ cursor: 'search:0:1', limit: 1, query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(mismatchSearch.status).toBe(400);
    const invalidFilters = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ filters: { unsupported: true }, limit: 1, query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(invalidFilters.status).toBe(400);
    const invalidSearchBodies = [
      { body: { query: '', scope: 'all', sourceView: 'published' }, detail: 'Search query must be a non-empty string.' },
      { body: { query: 'x'.repeat(501), scope: 'all', sourceView: 'published' }, detail: 'Search query must be 500 characters or fewer.' },
      { body: { query: 'порт', scope: 'bad-scope', sourceView: 'published' }, detail: 'Search scope is invalid.' },
      { body: { limit: 0, query: 'порт', scope: 'all', sourceView: 'published' }, detail: 'Search limit must be an integer between 1 and 50.' },
      { body: { filters: { bookId: 123 }, query: 'порт', scope: 'all', sourceView: 'published' }, detail: 'Search ID filters must be non-empty strings.' },
      { body: { filters: { chapterId: false }, query: 'порт', scope: 'all', sourceView: 'published' }, detail: 'Search ID filters must be non-empty strings.' },
      { body: { filters: { resultKinds: ['chapter', 'chapter'] }, query: 'порт', scope: 'all', sourceView: 'published' }, detail: 'Search resultKinds filter is invalid.' },
      {
        body: { filters: { resultKinds: ['chapter', 'annotation', 'unknown-kind'] }, query: 'порт', scope: 'all', sourceView: 'published' },
        detail: 'Search resultKinds filter is invalid.',
      },
      { body: { extra: true, query: 'порт', scope: 'all', sourceView: 'published' }, detail: 'Search request contains unsupported keys.' },
      { body: { query: 'порт', scope: 'all', signal: {}, sourceView: 'published' }, detail: 'Search request contains unsupported keys.' },
    ];
    for (const { body, detail } of invalidSearchBodies) {
      const invalidSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
      });
      expect(invalidSearch.status).toBe(400);
      await expect(invalidSearch.json()).resolves.toMatchObject({ detail, status: 400 });
    }

    const existingMemberDeniedInvite = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-mira/accept`, {
      method: 'POST',
      headers,
    });
    expect(existingMemberDeniedInvite.status).toBe(403);
    const mismatchInvite = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-mira/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member', 'mock-verified-email': 'other@example.com' },
    });
    expect(mismatchInvite.status).toBe(404);
    const inviteeDiscovery = await fetch(`${mswApiBaseUrl}/project-invitations?limit=10`, {
      headers: { ...headers, 'mock-actor-role': 'non_member', 'mock-verified-email': 'mira@example.com' },
    });
    expect(inviteeDiscovery.status).toBe(200);
    await expect(inviteeDiscovery.json()).resolves.toMatchObject({
      data: [expect.objectContaining({ email: 'mira@example.com', id: 'invitation-mira', status: 'pending' })],
    });
    const invitedNonMember = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-mira/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member', 'mock-verified-email': 'mira@example.com' },
    });
    expect(invitedNonMember.status).toBe(200);
    await expect(invitedNonMember.json()).resolves.toMatchObject({
      email: 'mira@example.com',
      id: 'member-invitation-mira',
      role: 'editor',
      status: 'active',
      userId: 'user-mira',
    });
    const membersAfterInvite = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members`, {
      headers,
    });
    await expect(membersAfterInvite.json()).resolves.toMatchObject({
      data: expect.arrayContaining([
        expect.objectContaining({ email: 'mira@example.com', id: 'member-invitation-mira', role: 'editor', status: 'active', userId: 'user-mira' }),
      ]),
    });
    const replayedMiraInvite = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-mira/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member', 'mock-verified-email': 'mira@example.com' },
    });
    expect(replayedMiraInvite.status).toBe(409);
    const unknownInvite = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-missing/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member' },
    });
    expect(unknownInvite.status).toBe(404);
    const accepted = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-1/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member' },
    });
    expect(accepted.status).toBe(404);
    const replayedInvite = await fetch(`${mswApiBaseUrl}/project-invitations/invitation-1/accept`, {
      method: 'POST',
      headers: { ...headers, 'mock-actor-role': 'non_member' },
    });
    expect(replayedInvite.status).toBe(404);
    const ownerInvite = await fetch(`${mswApiBaseUrl}/projects/project-white-port/invitations`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ email: 'owner-invite@example.test', role: 'owner' }),
    });
    expect(ownerInvite.status).toBe(400);
    const missingProjectMembers = await fetch(`${mswApiBaseUrl}/projects/project-missing/members`, {
      headers,
    });
    expect(missingProjectMembers.status).toBe(404);
    const missingProjectInvite = await fetch(`${mswApiBaseUrl}/projects/project-missing/invitations`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ email: 'new-collaborator@example.test', role: 'editor' }),
    });
    expect(missingProjectInvite.status).toBe(404);
    const missingMember = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-missing`, {
      method: 'PATCH',
      headers,
      body: JSON.stringify({ role: 'viewer' }),
    });
    expect(missingMember.status).toBe(404);
    const missingMemberAsViewer = await fetch(`${mswApiBaseUrl}/projects/project-white-port/members/member-missing`, {
      method: 'PATCH',
      headers: { ...headers, 'mock-actor-role': 'viewer' },
      body: JSON.stringify({ role: 'viewer' }),
    });
    expect(missingMemberAsViewer.status).toBe(404);
  });

  it('enforces MSW idempotency, async 202 headers and raw multipart parsing', async () => {
    mswServer.use(...createApiHandlers());
    const headers = {
      'content-type': 'application/json',
      cookie: 'ck_session=mock-session',
      origin: 'http://localhost:3000',
      'x-csrf-token': 'ck_msw_csrf_token_000000',
    };
    const missingKey = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(missingKey.status).toBe(400);

    const shortKey = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...headers, 'idempotency-key': 'short' },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(shortKey.status).toBe(400);
    const longKey = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...headers, 'idempotency-key': 'x'.repeat(129) },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(longKey.status).toBe(400);

    const firstIndex = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...headers, 'idempotency-key': 'idem-msw-index' },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(firstIndex.status).toBe(202);
    expect(firstIndex.headers.get('retry-after')).toBe('1');
    const firstBody = (await firstIndex.json()) as { jobId: string };
    expect(firstIndex.headers.get('location')).toBe(`/api/v1/jobs/${firstBody.jobId}`);

    const editorHeaders = { ...headers, mock_actor_role: 'editor' };
    const editorIndex = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...editorHeaders, 'idempotency-key': 'idem-msw-editor-index' },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(editorIndex.status).toBe(202);
    const editorIndexBody = (await editorIndex.json()) as { jobId: string };
    const editorCancel = await fetch(`${mswApiBaseUrl}/jobs/${editorIndexBody.jobId}/cancel`, {
      method: 'POST',
      headers: editorHeaders,
    });
    expect(editorCancel.status).toBe(200);
    await expect(editorCancel.json()).resolves.toMatchObject({ status: 'canceled' });

    const replayedIndex = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...headers, 'idempotency-key': 'idem-msw-index' },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(replayedIndex.status).toBe(202);
    expect(replayedIndex.headers.get('location')).toBe(`/api/v1/jobs/${firstBody.jobId}`);
    await expect(replayedIndex.json()).resolves.toMatchObject({ jobId: firstBody.jobId });

    const mismatchIndex = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...headers, 'idempotency-key': 'idem-msw-index' },
      body: JSON.stringify({ sourceView: 'draft' }),
    });
    expect(mismatchIndex.status).toBe(409);

    const boundary = 'ck-msw-raw-import';
    const multipartBody = rawMultipartImportBody(boundary, new Uint8Array([0xff]));
    const imported = await fetch(`${mswApiBaseUrl}/projects/project-white-port/imports`, {
      method: 'POST',
      headers: {
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
        'content-type': `multipart/form-data; boundary=${boundary}`,
        'idempotency-key': 'idem-msw-import-raw',
      },
      body: multipartBody,
    });
    expect(imported.status).toBe(202);
    expect(imported.headers.get('location')).toBeTruthy();
    expect(imported.headers.get('retry-after')).toBe('1');
    const importedBody = (await imported.json()) as { job: { result: null } };
    expect(importedBody.job.result).toBeNull();

    const changedMultipartBody = rawMultipartImportBody(boundary, new Uint8Array([0xfe]));
    const changedImport = await fetch(`${mswApiBaseUrl}/projects/project-white-port/imports`, {
      method: 'POST',
      headers: {
        cookie: 'ck_session=mock-session',
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
        'content-type': `multipart/form-data; boundary=${boundary}`,
        'idempotency-key': 'idem-msw-import-raw',
      },
      body: changedMultipartBody,
    });
    expect(changedImport.status).toBe(409);
  });

  it('keeps MSW job list, get and cancel behavior aligned with the repository', async () => {
    const repository = createMockApiClient();
    mswServer.use(...createApiHandlers({ repository }));
    const readHeaders = { cookie: 'ck_session=mock-session' };

    const firstPage = await fetch(`${mswApiBaseUrl}/projects/project-white-port/jobs?limit=1`, { headers: readHeaders });
    expect(firstPage.status).toBe(200);
    await expect(firstPage.json()).resolves.toEqual({
      data: [expect.objectContaining({ id: 'canon-text-index-job-1', kind: 'canon_text_index' })],
      meta: { hasMore: false, nextCursor: null },
    });

    const canonTextFilter = await fetch(
      `${mswApiBaseUrl}/projects/project-white-port/jobs?kind=canon_text_index&limit=1`,
      { headers: readHeaders },
    );
    expect(canonTextFilter.status).toBe(200);
    await expect(canonTextFilter.json()).resolves.toEqual({
      data: [expect.objectContaining({ id: 'canon-text-index-job-1' })],
      meta: { hasMore: false, nextCursor: null },
    });

    const viewerGet = await fetch(`${mswApiBaseUrl}/jobs/canon-text-index-job-1`, {
      headers: { ...readHeaders, mock_actor_role: 'viewer' },
    });
    await expect(viewerGet.json()).resolves.toMatchObject({ canCancel: false, links: { cancel: null } });
    const viewerCancel = await fetch(`${mswApiBaseUrl}/jobs/canon-text-index-job-1/cancel`, {
      method: 'POST',
      headers: {
        ...readHeaders,
        origin: 'http://localhost:3000',
        'x-csrf-token': 'ck_msw_csrf_token_000000',
        mock_actor_role: 'viewer',
      },
    });
    expect(viewerCancel.status).toBe(403);

    const mutationHeaders = {
      ...readHeaders,
      origin: 'http://localhost:3000',
      'x-csrf-token': 'ck_msw_csrf_token_000000',
      'content-type': 'application/json',
      mock_actor_role: 'editor',
    };
    const started = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...mutationHeaders, 'idempotency-key': 'idem-msw-job-parity' },
      body: JSON.stringify({ sourceView: 'published' }),
    });
    expect(started.status).toBe(202);
    const startedBody = (await started.json()) as { jobId: string; job: { result: unknown; links: { cancel: string | null } } };
    expect(startedBody.job).toMatchObject({
      result: null,
      links: { cancel: `/api/v1/jobs/${startedBody.jobId}/cancel` },
    });

    const indexingPage = await fetch(`${mswApiBaseUrl}/projects/project-white-port/jobs?kind=canon_text_index&limit=1`, {
      headers: readHeaders,
    });
    const indexingBody = (await indexingPage.json()) as {
      data: Array<{ id: string }>;
      meta: { hasMore: boolean; nextCursor: string | null };
    };
    expect(indexingBody).toMatchObject({ data: [{ id: startedBody.jobId }], meta: { hasMore: true } });
    expect(indexingBody.meta.nextCursor).toBeTruthy();
    const secondPage = await fetch(
      `${mswApiBaseUrl}/projects/project-white-port/jobs?kind=canon_text_index&limit=1&cursor=${encodeURIComponent(indexingBody.meta.nextCursor ?? '')}`,
      { headers: readHeaders },
    );
    await expect(secondPage.json()).resolves.toEqual({
      data: [expect.objectContaining({ id: 'canon-text-index-job-1' })],
      meta: { hasMore: false, nextCursor: null },
    });

    const creatorViewerCancel = await fetch(`${mswApiBaseUrl}/jobs/${startedBody.jobId}/cancel`, {
      method: 'POST',
      headers: { ...mutationHeaders, mock_actor_role: 'viewer' },
    });
    expect(creatorViewerCancel.status).toBe(200);
    await expect(creatorViewerCancel.json()).resolves.toMatchObject({
      status: 'canceled',
      result: null,
      canCancel: false,
      links: { cancel: null },
    });
  });

});

async function fetchWithOperationTimeout(url: string, init: RequestInit, operationId: string) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1000);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } catch (error) {
    throw new Error(`MSW handler did not resolve for ${operationId}: ${error instanceof Error ? error.message : String(error)}`);
  } finally {
    clearTimeout(timeout);
  }
}

function applyPathParams(path: string, operationPathParams: Record<string, string> = {}) {
  return path.replace(/\{([^}]+)\}/g, (_match, key: string) => operationPathParams[key] ?? pathParams[key] ?? key);
}

function routePathForOperation(operationId: OperationId, path: string, operationPathParams: Record<string, string> = {}) {
  if (operationId === 'takeReadingTestPair' || operationId === 'voteReadingTestPair') return applyPathParams(path, { slug: mockReadingTestSlug });
  if (operationId === 'deletePersonalNote' || operationId === 'deleteAuthorMemory' || operationId === 'deleteChapterDraft' || operationId === 'deleteAuthorMaterial') return `${applyPathParams(path, operationPathParams)}?expectedRevision=1`;
  if (operationId === 'listChatSessions') {
    return `${applyPathParams(path, operationPathParams)}?sourceView=published&scope=project`;
  }
  if (operationId === 'getSagaMapNode') {
    return `${applyPathParams(path, operationPathParams)}?sourceView=published&key=${encodeURIComponent('мара')}`;
  }
  if (operationId === 'searchSagaMap') {
    return `${applyPathParams(path, operationPathParams)}?sourceView=published&q=${encodeURIComponent('мара')}`;
  }
  if (['listSagaGlossary', 'getSagaMap', 'getSagaContents', 'listSagaMapHidden', 'hideSagaMapItem'].includes(operationId)) {
    return `${applyPathParams(path, operationPathParams)}?sourceView=published`;
  }
  if (operationId === 'removeProjectImageReference') {
    return `${applyPathParams(path, operationPathParams)}?subject=${encodeURIComponent('мара')}`;
  }
  if (operationId === 'getProjectImageFile') return `${applyPathParams(path, operationPathParams)}?size=thumb&v=0`;
  if (operationId === 'getBookExport') return `${applyPathParams(path, operationPathParams)}?format=docx`;
  return applyPathParams(path, operationPathParams);
}

function requestInit(operationId: OperationId, method: string): RequestInit {
  const headers = new Headers();
  headers.set('cookie', 'ck_session=mock-session');
  headers.set('origin', 'http://localhost:3000');
  headers.set('x-csrf-token', 'ck_msw_csrf_token_000000');
  headers.set('idempotency-key', `idem-msw-${operationId}`);
  if (operationId === 'acceptProjectInvitation' || operationId === 'listMyProjectInvitations') {
    headers.set('mock-actor-role', 'non_member');
    headers.set('mock-verified-email', 'mira@example.com');
  }

  if (method === 'GET' || method === 'DELETE') {
    return { method, headers };
  }
  if (operationId === 'acceptProjectInvitation') {
    return { method, headers };
  }

  if (operationId === 'importBookFile') {
    const boundary = 'ck-msw-parity';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="white-port.epub"',
        'Content-Type: application/epub+zip',
        '',
        'epub-content',
        `--${boundary}`,
        'Content-Disposition: form-data; name="metadata"',
        '',
        JSON.stringify({ sourceFileName: 'white-port.epub', title: 'White Port' }),
        `--${boundary}`,
        'Content-Disposition: form-data; name="options"',
        '',
        JSON.stringify({ importMode: 'new_book' }),
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    };
  }

  if (operationId === 'uploadImportQueueItem') {
    const boundary = 'ck-msw-import-queue';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [
        `--${boundary}`,
        'Content-Disposition: form-data; name="itemId"',
        '',
        'import-queue-1-item-1',
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="white-port.epub"',
        'Content-Type: application/epub+zip',
        '',
        'epub-content',
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    };
  }

  // A question with the author's chapter (specs/2026-11-07-chat-attach-chapter.md CA2): the question and a pasted chapter.
  if (operationId === 'createChatTurnWithChapter') {
    const boundary = 'ck-msw-chat-chapter';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [`--${boundary}`, 'Content-Disposition: form-data; name="body"', '', 'Ложится ли глава на канон?',
        `--${boundary}`, 'Content-Disposition: form-data; name="text"', '', 'Кайл шёл к реке.', `--${boundary}--`, ''].join('\r\n'),
    };
  }

  // The proofreading of a chapter of the author (specs/2026-11-07-writing-proofread.md, PR2): a text, and the revision of an open draft.
  if (operationId === 'startChapterProofread' || operationId === 'loadChapterDraftFile') {
    const boundary = 'ck-msw-proofread';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    const revision = operationId === 'loadChapterDraftFile' ? [`--${boundary}`, 'Content-Disposition: form-data; name="expectedRevision"', '', '1'] : [];
    return {
      method,
      headers,
      body: [...revision, `--${boundary}`, 'Content-Disposition: form-data; name="text"', '', 'Кайл шол к реке.', `--${boundary}--`, ''].join('\r\n'),
    };
  }

  if (operationId === 'startAuthorMaterialsImport') {
    const boundary = 'ck-msw-materials';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [`--${boundary}`, 'Content-Disposition: form-data; name="text"', '', 'Белый порт', `--${boundary}--`, ''].join('\r\n'),
    };
  }

  if (operationId === 'uploadProjectImage') {
    // The author's own image with the confirmation of the current edition (illustrations R15.1, R15.4).
    const boundary = 'ck-msw-image-upload';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="anna.png"',
        'Content-Type: image/png',
        '',
        'png-content',
        `--${boundary}`,
        'Content-Disposition: form-data; name="consentVersion"',
        '',
        '2026-11-04',
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    };
  }

  if (operationId === 'uploadFeedbackImage') {
    const boundary = 'ck-msw-feedback-image';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [
        `--${boundary}`,
        'Content-Disposition: form-data; name="draftKey"',
        '',
        '0123456789abcdef0123456789abcdef',
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="screenshot.png"',
        'Content-Type: image/png',
        '',
        'png-content',
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    };
  }

  if (operationId === 'uploadBookCover') {
    const boundary = 'ck-msw-book-cover';
    headers.set('content-type', `multipart/form-data; boundary=${boundary}`);
    return {
      method,
      headers,
      body: [
        `--${boundary}`,
        'Content-Disposition: form-data; name="file"; filename="cover.png"',
        'Content-Type: image/png',
        '',
        'png-content',
        `--${boundary}--`,
        '',
      ].join('\r\n'),
    };
  }

  headers.set('content-type', 'application/json');
  return { method, headers, body: JSON.stringify(bodyFor(operationId)) };
}

function bodyFor(operationId: OperationId): unknown {
  switch (operationId) {
    case 'createBook':
      return { title: 'Новая книга' };
    case 'reportClientError':
      return { browser: 'chrome', build: 'index-test.js', kind: 'error', mobile: false, name: 'TypeError', surface: 'chat' };
    case 'createImportQueue':
      return {
        items: [{
          clientItemId: 'msw-import-client-1',
          position: 1,
          sourceFileName: 'white-port.epub',
          detectedBookOrder: 5,
        }],
      };
    case 'createPersonalNote':
      return { body: 'Личная запись' };
    case 'updatePersonalNote':
      return { body: 'Изменённая запись', expectedRevision: 1 };
    case 'createChatSession':
      return { scope: 'project', sourceView: 'published', title: 'Исследование порта' };
    case 'createChatTurn':
      return { body: 'Кто первым заметил дым?' };
    case 'steerChatTurn':
      return { body: 'Без дракона' };
    case 'createProject':
      return { description: 'Черновой проект', title: 'Новый проект' };
    case 'createProjectInvitation':
      return { email: 'coauthor@example.test', role: 'editor' };
    case 'loginUser':
      return { email: 'mira@example.com', password: 'white-port-12', rememberMe: true };
    case 'openDesktopSession':
      return { code: 'ck_open_code_example_0000000000000000000000000' };
    case 'registerUser':
      return { acceptedTerms: true, acceptedPersonalDataConsent: true, displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' };
    case 'confirmEmailVerification':
      return { token: mockAccountToken('email_verification', 'user-mira') };
    case 'requestPasswordReset':
      return { email: 'mira@example.com' };
    case 'changePassword':
      return { currentPassword: 'white-port-12', newPassword: 'white-port-13' };
    case 'acceptLegalDocuments':
      return { acceptedTerms: true, acceptedPersonalDataConsent: true };
    case 'setAccountVoice':
      return { voice: 'spruce' };
    case 'resetPassword':
      return { token: mockAccountToken('password_reset', 'user-mira'), newPassword: 'white-port-12' };
    case 'startImpersonation':
      return { email: 'target@example.test' };
    case 'searchChapters':
      return { limit: 10, query: '458', sourceView: 'published' };
    case 'searchProject':
      return { limit: 10, query: 'порт', scope: 'all', sourceView: 'published' };
    case 'startProjectIndexing':
      return { sourceView: 'published' };
    case 'startImportQueue':
      return { expectedVersion: 3 };
    case 'updateBook':
      return { title: 'Обновленная книга' };
    case 'setBookCoverFromImage':
      return { imageNumber: 1 };
    case 'updateImportQueue':
      return {
        expectedVersion: 2,
        items: [{ id: 'import-queue-1-item-1', position: 1, confirmedBookOrder: 5 }],
      };
    case 'updateProject':
      return { description: 'Обновленный проект', title: 'Обновленный проект' };
    case 'updateProjectMemberRole':
      return { role: 'viewer' };
    case 'updateChatSession':
      return { title: 'Обновленный чат' };
    case 'createChapterDraft':
      return { kind: 'sketch', title: 'Глава', text: 'Набросок' };
    case 'updateChapterDraft':
      return { expectedRevision: 1, text: 'Набросок' };
    case 'startChapterDraftJob':
      return { expectedRevision: 1, mode: 'check' };
    case 'addChapterDraftToBook':
      return { expectedRevision: 2 };
    case 'replaceChapterFromDraft':
      return { expectedRevision: 2, title: 'Белый порт' };
    case 'revertChapterFromDraft':
      return { expectedRevision: 3 };
    case 'reopenChapterFromBook':
      return { expectedRevision: 1 };
    case 'applyChapterDraftNote':
      return { expectedRevision: 2 };
    case 'applyChapterDraftNotes':
      // The first draft of a fresh mock and the first correction of its proofreading.
      return { expectedRevision: 1, notes: ['draft-1-note-1'] };
    case 'takeChapterDraftVersion':
      return { expectedRevision: 3, whole: true };
    case 'updateChapterDraftPlan':
      return {
        expectedNumber: 1,
        scenes: [{
          number: 1, title: 'У ворот', state: 'planned', pov: 'Кира', whereWhen: 'ночь', participants: ['Кира'], goal: 'уйти', question: 'уйдёт?',
          resistance: 'стража', turns: ['окрик'], outcome: { type: 'yes_cost', what: 'ранена' }, value: { name: 'свобода', from: '−', to: '+' },
          feelings: { main: 'страх' }, end: 'outcome', words: 900, beats: [{ type: 'action', stimulus: 'окрик', reaction: 'бег', atStake: 'успеет ли' }],
        }],
      };
    case 'updateChapterDraftMoment':
      return { expectedRevision: 1, choices: [{ action: 'exclude', entry: 'Ольм' }] };
    case 'updateChapterDraftNote':
      return { status: 'dismissed' };
    case 'submitDraftTurn':
      return { text: 'Сверь черновик с каноном' };
    case 'hideSagaMapItem':
      return { nodeKey: 'мара' };
    case 'createAuthorMemory':
      return { kind: 'plan', text: 'Кира узнает правду' };
    case 'updateAuthorMemory':
      return { kind: 'plan', text: 'Кира узнает правду', expectedRevision: 1 };
    case 'updateAuthorMaterial':
      return { name: 'оживший кошмар', kind: 'skill', description: 'Сны', properties: [], expectedRevision: 1 };
    case 'confirmAuthorMaterialsImport':
      return { items: [{ index: 0 }] };
    case 'updateProjectImageDetails':
      return { description: 'Мара у пристани', chapter: 'b2c12', subjects: [{ key: 'мара', name: 'Мара' }], sourceView: 'published' };
    case 'setProjectImageReference':
      return { subjectKey: 'мара', subjectName: 'Мара', fromChapter: 'b2c12', sourceView: 'published' };
    case 'updateProjectIllustrationStyle':
      return { style: 'гравюра' };
    case 'deleteProjectImages':
      return { numbers: [2] };
    case 'createProjectImageLayer':
      return { instruction: 'Сова вместо ворона.', region: { x: 0.5, y: 0.1, w: 0.25, h: 0.25 } };
    case 'updateProjectImageLayer':
      return { visible: false };
    case 'createProjectCharacterSheet':
      return { subject: 'Мара' };
    case 'createDrawing':
      return { idea: 'Дракон на скале', kind: 'character', selfDescribed: true };
    case 'updateDrawing':
      return { idea: 'Дракон с двумя рогами на скале' };
    case 'drawDrawing':
      return { variants: 1, expectedRevision: 1 };
    case 'submitDrawingTurn':
      return { body: 'Два рога и мелкая чешуя' };
    case 'setAdminWriterQuota':
      return { chat_turns_per_day: 60, active_turns: 'default' };
    case 'setAdminWriterIllustrations':
      return { state: 'active', imagesPerDay: 40 };
    case 'setAdminQuotaDefaults':
      return { chat_turns_per_day: 40, trial_drawings: 'default' };
    case 'createIllustrationAccessRequest':
      return { message: 'Иллюстрации к главам' };
    case 'createAdminPromoPermission':
      return {
        projectId: 'project-white-port', recipientEmail: null,
        places: ['canonkeeper_home'], attribution: 'Мира Волкова', message: null,
      };
    case 'takeAdminPromoFragment':
      return { chapter: 'b2c12', from: 2, to: 3 };
    case 'answerPromoPermission':
      return { decision: 'allow', attribution: null };
    case 'takeReadingTestPair':
      return { voterId: '0123456789abcdef0123456789abcdef' };
    case 'voteReadingTestPair':
      return { voterId: '0123456789abcdef0123456789abcdef', pairNumber: 1, choice: 'first' };
    case 'readPromoLink':
      return { token: mockPromoAnswerLink };
    case 'answerPromoLink':
      return { token: mockPromoAnswerLink, decision: 'decline', attribution: null };
    case 'revokePromoLink':
      return { token: mockPromoRevokeLink };
    case 'setAdminContacts':
      return { telegram: '@canon_keeper', email: 'support@example.test', note: 'Отвечаем в течение дня' };
    case 'createFeedbackThread':
      return {
        draftKey: 'abcdef0123456789abcdef0123456789', topic: 'question', blocks: [{ type: 'text', text: 'Как добавить книгу?' }],
        context: { page: '/manuscript/books', browser: 'chrome', mobile: false, width: 1440 },
      };
    case 'addFeedbackMessage':
      return {
        draftKey: 'abcdef0123456789abcdef0123456789', blocks: [{ type: 'text', text: 'Уточнение' }],
        context: { page: '/manuscript/books', browser: 'chrome', mobile: false, width: 1440 },
      };
    case 'replyAdminFeedback':
      return { draftKey: 'abcdef0123456789abcdef0123456789', blocks: [{ type: 'text', text: 'Ответ' }] };
    case 'setAdminFeedbackStatus':
      return { status: 'answered' };
    default:
      return {};
  }
}

async function prepareRepositoryForOperation(
  repository: ReturnType<typeof createMockApiClient>,
  operationId: OperationId,
): Promise<Record<string, string>> {
  switch (operationId) {
    case 'getFeedbackThread':
    case 'addFeedbackMessage':
    case 'getFeedbackImage':
    case 'getAdminFeedbackThread':
    case 'replyAdminFeedback':
    case 'setAdminFeedbackStatus': {
      const draftKey = '0123456789abcdef0123456789abcdef';
      const image = await repository.uploadFeedbackImage({ file: new File([new Uint8Array([137, 80, 78, 71])], 'shot.png', { type: 'image/png' }), draftKey });
      const thread = await repository.createFeedbackThread({
        draftKey, topic: 'bug', blocks: [{ type: 'text', text: 'Глава не открывается' }, { type: 'image', image: { id: image.id } }],
        context: { page: '/writing', browser: 'chrome', mobile: false, width: 1440 },
      });
      return { threadId: thread.id, imageId: image.id };
    }
    case 'chooseProjectImageVariant': {
      const first = await repository.agentDrawVersion('project-white-port', 1, { instruction: 'ночь', variants: 2 });
      for (let read = 0; read < 7; read += 1) await repository.getProjectImage('project-white-port', first.number);
      return { number: String(first.number) };
    }
    case 'reopenChapterFromBook':
      return { draftId: (await repository.openChapterEdit('project-white-port', 'chapter-16')).id };
    case 'replaceChapterFromDraft':
    case 'revertChapterFromDraft': {
      const draft = await repository.openChapterEdit('project-white-port', 'chapter-16');
      await repository.updateChapterDraft('project-white-port', draft.id, { expectedRevision: draft.revision, text: 'Новый текст главы.' });
      if (operationId === 'revertChapterFromDraft') {
        await repository.replaceChapterFromDraft('project-white-port', draft.id, { expectedRevision: draft.revision + 1, title: 'Белый порт' });
      }
      return { draftId: draft.id };
    }
    case 'getChapterDraftPlan':
    case 'updateChapterDraftPlan':
    case 'updateChapterDraftMoment':
    case 'getChapterDraftMoment': {
      const draft = await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', idea: 'Кира уходит.' }, { idempotencyKey: `idem-msw-draft-${operationId}` });
      if (operationId === 'getChapterDraftPlan' || operationId === 'updateChapterDraftPlan') await repository.helperPlanDraft('project-white-port', draft.id);
      return { draftId: draft.id };
    }
    case 'addChapterDraftToBook': {
      const draft = await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', text: 'Набросок' }, { idempotencyKey: 'idem-msw-draft-add-to-book' });
      const book = (await repository.listBooks('project-white-port')).data[0]!;
      await repository.updateChapterDraft('project-white-port', draft.id, { expectedRevision: 1, text: 'Текст', place: { bookId: book.id } });
      return { draftId: draft.id };
    }
    case 'listChapterDraftVersions':
    case 'getChapterDraftVersion':
    case 'takeChapterDraftVersion': {
      // «Применить» keeps the text before it: the draft has version 1 (specs/2026-11-06-writing-agent.md, WA7.2).
      const draft = await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', text: 'Набросок' }, { idempotencyKey: `idem-msw-draft-${operationId}` });
      await repository.updateChapterDraft('project-white-port', draft.id, { expectedRevision: 1, text: 'Кира взял меч.' });
      const note = await repository.helperNoteDraft('project-white-port', draft.id, { place: 'Кира взял', body: 'Кира — она', replacement: 'Кира взяла' });
      await repository.applyChapterDraftNote('project-white-port', draft.id, note.id, { expectedRevision: 2 });
      return { draftId: draft.id, number: '1' };
    }
    case 'loadChapterDraftFile':
      return { draftId: (await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', text: 'Набросок' }, { idempotencyKey: 'idem-msw-draft-file' })).id };
    case 'applyChapterDraftNotes':
    case 'getChapterDraftExport': {
      const draft = await repository.startChapterProofread('project-white-port', { text: 'Кайл шол к реке.' }, { idempotencyKey: `idem-msw-proofread-${operationId}` });
      for (let read = 0; read < 4; read += 1) await repository.getChapterDraft('project-white-port', draft.id);
      return { draftId: draft.id };
    }
    case 'applyChapterDraftNote':
    case 'updateChapterDraftNote': {
      const draft = await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', text: 'Набросок' }, { idempotencyKey: `idem-msw-draft-${operationId}` });
      await repository.updateChapterDraft('project-white-port', draft.id, { expectedRevision: 1, text: 'Кира взял меч.' });
      const note = await repository.helperNoteDraft('project-white-port', draft.id, { place: 'Кира взял', body: 'Кира — она', replacement: 'Кира взяла' });
      return { draftId: draft.id, noteId: note.id };
    }
    case 'getDrawing':
    case 'updateDrawing':
    case 'deleteDrawing':
    case 'drawDrawing':
    case 'submitDrawingTurn':
      return {
        drawingId: (await repository.createDrawing('project-white-port', { idea: 'Дракон на скале', selfDescribed: true }, { idempotencyKey: `idem-msw-drawing-${operationId}` })).id,
      };
    case 'chooseProjectImageLayerVariant': {
      const image = await repository.createProjectImageLayer('project-white-port', 1, { instruction: 'Сова', region: { x: 0.5, y: 0.1, w: 0.25, h: 0.25 } }, { idempotencyKey: 'idem-msw-layer-choice' });
      return { number: '1', layerId: image.layers.at(-1)!.id };
    }
    case 'getChapterDraft':
    case 'updateChapterDraft':
    case 'deleteChapterDraft':
    case 'startChapterDraftJob':
    case 'submitDraftTurn':
      return {
        draftId: (await repository.createChapterDraft('project-white-port', { kind: 'sketch', title: 'Глава', text: 'Набросок' }, { idempotencyKey: `idem-msw-draft-${operationId}` })).id,
      };
    case 'confirmAuthorMaterialsImport':
    case 'discardAuthorMaterialsImport': {
      const started = await repository.startAuthorMaterialsImport('project-white-port', { text: 'Белый порт' }, { idempotencyKey: 'idem-msw-materials-fixture' });
      await repository.getAuthorMaterialsImport('project-white-port');
      return { importId: started.id };
    }
    case 'updateAuthorMaterial':
    case 'deleteAuthorMaterial': {
      const started = await repository.startAuthorMaterialsImport('project-white-port', { text: 'Белый порт' }, { idempotencyKey: 'idem-msw-materials-fixture' });
      await repository.getAuthorMaterialsImport('project-white-port');
      await repository.confirmAuthorMaterialsImport('project-white-port', started.id, { items: [{ index: 0 }] }, { idempotencyKey: 'idem-msw-materials-answer' });
      return { materialId: (await repository.listAuthorMaterials('project-white-port')).data[0]!.id };
    }
    case 'restoreSagaMapItem':
      return { hiddenId: (await repository.hideSagaMapItem('project-white-port', 'published', { nodeKey: 'мара' })).id };
    case 'updateAuthorMemory':
    case 'deleteAuthorMemory':
      return {
        recordId: (await repository.createAuthorMemory('project-white-port', { kind: 'plan', text: 'Запись для проверки' }, { idempotencyKey: 'idem-msw-memory-fixture' })).id,
      };
    case 'getPersonalNote':
    case 'updatePersonalNote':
    case 'deletePersonalNote':
    case 'resolvePersonalNoteSource':
      return { noteId: (await repository.createPersonalNote({ body: 'Запись для проверки' }, { idempotencyKey: 'idem-msw-notes-fixture' })).id };
    case 'createChatTurn':
    case 'createChatTurnWithChapter':
    case 'deleteChatSession':
    case 'getChatSession':
    case 'updateChatSession':
      return { chatId: await createChatFixture(repository) };
    case 'getChatAttachment': {
      const chatId = await createChatFixture(repository);
      await repository.createChatTurnWithChapter(chatId, { body: 'Ложится ли глава на канон?', text: 'Кайл шёл к реке.' }, { idempotencyKey: 'idem-msw-attachment-fixture' });
      return { chatId, number: '1' };
    }
    case 'steerChatTurn': {
      const chatId = await createChatFixture(repository);
      const { job } = await repository.createChatTurn(chatId, { body: 'Кто первым заметил дым?' }, { idempotencyKey: 'idem-msw-steer-fixture' });
      return { chatId, turnId: job.subject.type === 'research_chat_turn' ? job.subject.turnId : '' };
    }
    case 'resolveChatChapterKey':
      return { chatId: await createChatFixture(repository), chapterKey: 'b2c12' };
    case 'deleteBookCover':
    case 'getBookCover':
      await repository.uploadBookCover(pathParams.bookId!, new File(['cover'], 'cover.png', { type: 'image/png' }));
      return { version: '1' };
    case 'getImportQueue':
    case 'uploadImportQueueItem':
    case 'updateImportQueue':
    case 'cancelImportQueue':
      return { queueId: await createImportQueueFixture(repository) };
    case 'startImportQueue': {
      // Like the backend, the queue starts only after its volume number is confirmed.
      const queueId = await createImportQueueFixture(repository);
      await repository.updateImportQueue(pathParams.projectId!, queueId, {
        expectedVersion: 2,
        items: [{ id: `${queueId}-item-1`, position: 1, confirmedBookOrder: 5 }],
      });
      return { queueId };
    }
    case 'updateProjectImageLayer':
    case 'deleteProjectImageLayer': {
      const image = await repository.createProjectImageLayer(pathParams.projectId!, 1, {
        instruction: 'Сова вместо ворона.', region: { x: 0.5, y: 0.1, w: 0.25, h: 0.25 },
      }, { idempotencyKey: 'idem-msw-image-layer-fixture' });
      return { layerId: image.layers[0]!.id };
    }
    case 'getAdminPromoIllustration':
    case 'takeAdminPromoFragment':
    case 'revokePromoPermission':
      await repository.answerPromoPermission(pathParams.permissionId!, { decision: 'allow', attribution: null });
      return {};
    case 'revokePromoLink':
      await repository.answerPromoLink({ token: mockPromoAnswerLink, decision: 'allow', attribution: null });
      return {};
    case 'restoreProject':
      await repository.deleteProject(pathParams.projectId!);
      return {};
    case 'restoreBook':
      await repository.deleteBook(pathParams.bookId!);
      return {};
    case 'restoreChapter':
      await repository.deleteChapter(pathParams.chapterId!);
      return {};
    default:
      return {};
  }
}

async function createImportQueueFixture(repository: ReturnType<typeof createMockApiClient>) {
  const queue = await repository.createImportQueue(pathParams.projectId!, {
    items: [{
      clientItemId: 'msw-import-fixture',
      position: 1,
      sourceFileName: 'white-port.epub',
      detectedBookOrder: 5,
    }],
  }, { idempotencyKey: 'idem-msw-import-queue-fixture' });
  await repository.uploadImportQueueItem(pathParams.projectId!, queue.id, {
    itemId: queue.items[0]!.id,
    file: { name: 'white-port.epub', contentType: 'application/epub+zip', sizeBytes: 12 },
  }, { idempotencyKey: 'idem-msw-import-upload-fixture' });
  return queue.id;
}

async function createChatFixture(repository: ReturnType<typeof createMockApiClient>) {
  const chat = await repository.createChatSession(
    pathParams.projectId!,
    { scope: 'project', sourceView: 'published', title: 'Исследование порта' },
    { idempotencyKey: 'msw-chat-fixture' },
  );
  return chat.id;
}

const expectedSuccessStatuses: Partial<Record<OperationId, number>> = {
  uploadFeedbackImage: 201,
  createFeedbackThread: 201,
  addFeedbackMessage: 201,
  replyAdminFeedback: 201,
  createAuthorMemory: 201,
  hideSagaMapItem: 201,
  restoreSagaMapItem: 204,
  deleteAuthorMemory: 204,
  deleteAuthorMaterial: 204,
  discardAuthorMaterialsImport: 204,
  startAuthorMaterialsImport: 202,
  startChapterProofread: 201,
  createChapterDraft: 201,
  deleteChapterDraft: 204,
  startChapterDraftJob: 201,
  createPersonalNote: 201,
  deletePersonalNote: 204,
  registerUser: 201,
  sendEmailVerification: 202,
  requestPasswordReset: 202,
  changePassword: 204,
  logoutEverywhere: 204,
  acceptLegalDocuments: 204,
  createProject: 201,
  createBook: 201,
  createImportQueue: 201,
  importBookFile: 202,
  startImportQueue: 202,
  startProjectIndexing: 202,
  createChatSession: 201,
  createChatTurn: 202,
  createChatTurnWithChapter: 202,
  steerChatTurn: 202,
  createProjectInvitation: 201,
  logoutUser: 204,
  reportClientError: 204,
  deleteProject: 204,
  deleteBook: 204,
  deleteChapter: 204,
  removeProjectMember: 204,
  deleteChatSession: 204,
  deleteProjectImage: 204,
  cutoutProjectImage: 201,
  createProjectImageVariant: 201,
  enlargeProjectImage: 201,
  createProjectImageLayer: 201,
  createProjectCharacterSheet: 201,
  createIllustrationStyleSketches: 201,
  uploadProjectImage: 201,
  createIllustrationAccessRequest: 201,
  createAdminPromoPermission: 201,
  createDrawing: 201,
  deleteDrawing: 204,
  drawDrawing: 201,
  submitDrawingTurn: 202,
  submitDraftTurn: 202,
};

function expectedSuccessStatus(operationId: OperationId) {
  return expectedSuccessStatuses[operationId] ?? 200;
}

async function expectGeneratedOperationResponse(operationId: OperationId, response: Response) {
  const expectedStatus = expectedSuccessStatus(operationId);
  expect(response.status, `${operationId}: ${await response.clone().text()}`).toBe(expectedStatus);
  if (operationId === 'getBookCover' || operationId === 'getProjectImageFile') {
    expect(response.headers.get('cache-control'), operationId).toBe('private, max-age=31536000, immutable');
    expect(response.headers.get('content-type'), operationId).toContain('image/png');
    expect((await response.arrayBuffer()).byteLength, operationId).toBeGreaterThan(0);
    return;
  }
  expect(response.headers.get('cache-control'), operationId).toBe(operationId === 'getFeedbackImage' ? 'private, max-age=3600' : 'no-store');
  if (operationId === 'getBookExport' || operationId === 'getChapterDraftExport') {
    expect(response.headers.get('content-disposition'), operationId).toContain("attachment; filename*=utf-8''");
    expect((await response.arrayBuffer()).byteLength, operationId).toBeGreaterThan(0);
    return;
  }
  if (operationId === 'getFeedbackImage') {
    expect(response.headers.get('content-type'), operationId).toContain('image/png');
    expect((await response.arrayBuffer()).byteLength, operationId).toBeGreaterThan(0);
    return;
  }
  if (operationId === 'getAdminPromoIllustration') {
    expect(response.headers.get('content-type'), operationId).toContain('image/png');
    expect((await response.arrayBuffer()).byteLength, operationId).toBeGreaterThan(0);
    return;
  }
  if (expectedStatus === 204) {
    await expect(response.text(), operationId).resolves.toBe('');
    return;
  }
  expect(response.headers.get('content-type'), operationId).toContain('json');
  const body = (await response.json()) as unknown;
  expect(body, operationId).toBeTypeOf('object');
}
