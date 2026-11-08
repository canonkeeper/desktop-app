import { afterEach, describe, expect, it, vi } from 'vitest';
import { NetworkApiError, createCanonKeeperApiClient } from './index';
import type { Book, BookCoverUploadOptions, CanonKeeperApiClient } from './index';
import { operationManifest } from './generated/operation-manifest';

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function csrfResponse() {
  return jsonResponse({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-06-16T05:10:00.000Z', rotation: 'refreshed' });
}

afterEach(() => vi.unstubAllGlobals());

describe('OpenApiCanonKeeperClient', () => {
  function assertRequiredIdempotencyTypes(api: CanonKeeperApiClient) {
    if (Date.now() < 0) {
      // @ts-expect-error Для идемпотентных команд обязателен options с idempotencyKey.
      void api.createChatTurn('chat_1', { body: 'Вопрос' });
    }
  }

  void assertRequiredIdempotencyTypes;

  function assertPhase13ClientTypes(api: CanonKeeperApiClient, file: File) {
    if (Date.now() < 0) {
      const options: BookCoverUploadOptions = { signal: new AbortController().signal };
      const uploaded: Promise<Book> = api.uploadBookCover('book_1', file, options);
      const deleted: Promise<Book> = api.deleteBookCover('book_1');
      const renamed = api.renameChatSession('chat_1', { title: 'Новое название' });
      void [uploaded, deleted, renamed];
    }
  }

  void assertPhase13ClientTypes;

  it('exposes cover upload, cover delete and Chat rename operations', () => {
    const api = createCanonKeeperApiClient();

    expect(api.uploadBookCover).toBeTypeOf('function');
    expect(api.deleteBookCover).toBeTypeOf('function');
    expect(api.renameChatSession).toBeTypeOf('function');
  });

  it('resets CSRF after an ambiguous impersonation transition failure', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return jsonResponse({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-08-16T12:00:00.000Z', rotation: 'refreshed' });
        }
        if (request.method === 'POST') {
          return jsonResponse({ code: 'server.error', detail: 'ambiguous' }, 500);
        }
        return jsonResponse({ user: { id: 'admin-id', displayName: 'Admin', email: 'admin@example.com', emailVerified: true, createdAt: '2026-08-16T00:00:00.000Z' } });
      },
    });

    await expect(api.request('startImpersonation', { body: { email: 'target@example.com' } })).rejects.toBeDefined();
    await api.request('stopImpersonation');

    expect(calls.filter((call) => call.url.endsWith('/auth/csrf'))).toHaveLength(2);
  });

  it('uses the typed impersonation start and stop operations', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) return csrfResponse();
        return jsonResponse({ user: { id: 'target-id', displayName: 'Target', email: 'target@example.com', emailVerified: true, createdAt: '2026-08-16T00:00:00.000Z' } });
      },
    });

    await api.startImpersonation({ email: 'target@example.com' });
    await api.stopImpersonation();

    const operations = calls.filter((call) => !call.url.endsWith('/auth/csrf'));
    expect(operations.map((call) => [call.method, call.url])).toEqual([
      ['POST', 'https://api.example.test/api/v1/auth/impersonation'],
      ['DELETE', 'https://api.example.test/api/v1/auth/impersonation'],
    ]);
    await expect(operations[0]?.clone().json()).resolves.toEqual({ email: 'target@example.com' });
  });

  it('sends JSON requests through the generated path manifest with credentials included', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return csrfResponse();
        }
        return jsonResponse({
          user: { id: 'user_1', displayName: 'Мира Волкова', email: 'mira@example.com', emailVerified: true },
        });
      },
    });

    const sampleCredential = ['test', 'credential'].join('-');

    await api.login({ email: 'mira@example.com', password: sampleCredential, rememberMe: true });

    expect(calls).toHaveLength(2);
    const csrfCall = calls[0];
    const call = calls[1];
    expect(csrfCall?.url).toBe('https://api.example.test/api/v1/auth/csrf');
    expect(csrfCall?.method).toBe('GET');
    if (!call) {
      throw new Error('Expected login request to be recorded.');
    }
    expect(call.url).toBe('https://api.example.test/api/v1/auth/login');
    expect(call.method).toBe('POST');
    expect(call.credentials).toBe('include');
    expect(call.headers.get('X-CSRF-Token')).toBe('csrf_test_token_12345');
    await expect(call.clone().json()).resolves.toEqual({
      email: 'mira@example.com',
      password: sampleCredential,
      rememberMe: true,
    });
  });

  it('routes session, member and invitation convenience methods through generated operations', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return csrfResponse();
        }
        if (request.method === 'DELETE') {
          return new Response(null, { status: 204 });
        }
        if (request.url.endsWith('/projects/project_1/invitations') && request.method === 'POST') {
          return jsonResponse({ id: 'invitation_1', projectId: 'project_1', email: 'coauthor@example.test', role: 'editor', status: 'pending' }, 201);
        }
        if (request.url.endsWith('/members/member_1')) {
          return jsonResponse({ id: 'member_1', projectId: 'project_1', role: 'viewer' });
        }
        if (request.url.endsWith('/accept')) {
          return jsonResponse({ id: 'member_invitation_1', projectId: 'project_1', role: 'editor' });
        }
        if (request.url.endsWith('/cancel')) {
          return jsonResponse({ id: 'invitation_1', projectId: 'project_1', status: 'canceled' });
        }
        if (request.url.endsWith('/auth/session/rotate')) {
          return jsonResponse({ user: { id: 'user_1', displayName: 'Мира Волкова', email: 'mira@example.com', emailVerified: true } });
        }
        return jsonResponse({ data: [] });
      },
    });

    await api.rotateSession();
    await api.listProjectMembers('project_1');
    await api.createProjectInvitation('project_1', { email: 'coauthor@example.test', role: 'editor' });
    await api.listProjectInvitations('project_1');
    await api.listMyProjectInvitations();
    await api.updateProjectMemberRole('project_1', 'member_1', { role: 'viewer' });
    await api.removeProjectMember('project_1', 'member_1');
    await api.cancelProjectInvitation('invitation_1');
    await api.acceptProjectInvitation('invitation_1');

    const operationCalls = calls.filter((call) => !call.url.endsWith('/auth/csrf'));
    expect(operationCalls.map((call) => [call.method, call.url])).toEqual([
      ['POST', 'https://api.example.test/api/v1/auth/session/rotate'],
      ['GET', 'https://api.example.test/api/v1/projects/project_1/members'],
      ['POST', 'https://api.example.test/api/v1/projects/project_1/invitations'],
      ['GET', 'https://api.example.test/api/v1/projects/project_1/invitations'],
      ['GET', 'https://api.example.test/api/v1/project-invitations'],
      ['PATCH', 'https://api.example.test/api/v1/projects/project_1/members/member_1'],
      ['DELETE', 'https://api.example.test/api/v1/projects/project_1/members/member_1'],
      ['POST', 'https://api.example.test/api/v1/project-invitations/invitation_1/cancel'],
      ['POST', 'https://api.example.test/api/v1/project-invitations/invitation_1/accept'],
    ]);
    expect(operationCalls.every((call) => call.credentials === 'include')).toBe(true);
    const createInvitationCall = operationCalls[2];
    const updateMemberCall = operationCalls[5];
    const acceptInvitationCall = operationCalls[8];
    if (!createInvitationCall || !updateMemberCall || !acceptInvitationCall) {
      throw new Error('Expected member and invitation wrapper requests to be recorded.');
    }
    await expect(createInvitationCall.clone().json()).resolves.toEqual({ email: 'coauthor@example.test', role: 'editor' });
    await expect(updateMemberCall.clone().json()).resolves.toEqual({ role: 'viewer' });
    await expect(acceptInvitationCall.clone().text()).resolves.toBe('');
  });

  it('passes project job list query options through the generated operation', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        return jsonResponse({ data: [], meta: { hasMore: false, nextCursor: null } });
      },
    });

    await api.listProjectJobs('project_1', { cursor: 'jobs:abcd1234:1', kind: 'canon_text_index', limit: 1 });

    const call = calls[0];
    if (!call) {
      throw new Error('Expected listProjectJobs request to be recorded.');
    }
    const url = new URL(call.url);
    expect(call.method).toBe('GET');
    expect(url.pathname).toBe('/api/v1/projects/project_1/jobs');
    expect(url.searchParams.get('kind')).toBe('canon_text_index');
    expect(url.searchParams.get('limit')).toBe('1');
    expect(url.searchParams.get('cursor')).toBe('jobs:abcd1234:1');
  });

  it('gets the project support snapshot through the generated operation', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        return jsonResponse({
          schemaVersion: 'CKProjectSupportSnapshotV1',
          deletionTasks: [],
          artifactOwners: [],
          deletionTaskAges: [
            { boundary: 'oldest', bucket: 'none' },
            { boundary: 'newest', bucket: 'none' },
          ],
        });
      },
    });

    const snapshot = await api.getProjectSupportSnapshot('project_1');

    expect(snapshot.schemaVersion).toBe('CKProjectSupportSnapshotV1');
    expect(calls).toHaveLength(1);
    expect(calls[0]?.method).toBe('GET');
    expect(calls[0]?.url).toBe('https://api.example.test/api/v1/projects/project_1/support-snapshot');
  });

  it('passes exact source views to indexing status operations', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        return jsonResponse({});
      },
    });

    await api.getIndexingStatus('project_1');
    await api.getIndexingStatus('project_1', { bookId: 'book_1', sourceView: 'draft' });

    expect(calls[0]?.url).toBe('https://api.example.test/api/v1/projects/project_1/indexing-status');
    expect(calls[1]?.url).toBe('https://api.example.test/api/v1/projects/project_1/indexing-status?bookId=book_1&sourceView=draft');
  });

  it('passes cursor list query options through wrappers and protects searchProject with CSRF', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return csrfResponse();
        }
        return jsonResponse({ data: [], meta: { hasMore: false, nextCursor: null } });
      },
    });

    await api.listBooks('project_1', { cursor: 'list:listBooks:abcd1234:1', limit: 1 });
    await api.listProjectJobs('project_1', { kind: 'canon_text_index', limit: 2 });
    await api.listChapters('book_1', { limit: 3 });
    await api.searchProject('project_1', 'port', 'published', 'all', { limit: 1 });

    const booksCall = calls[0];
    const jobsCall = calls[1];
    const chaptersCall = calls[2];
    const csrfCall = calls[3];
    const searchCall = calls[4];
    if (!booksCall || !jobsCall || !chaptersCall || !csrfCall || !searchCall) {
      throw new Error('Expected list/search wrapper requests to be recorded.');
    }
    const booksUrl = new URL(booksCall.url);
    expect(booksCall.method).toBe('GET');
    expect(booksUrl.pathname).toBe('/api/v1/projects/project_1/books');
    expect(booksUrl.searchParams.get('limit')).toBe('1');
    expect(booksUrl.searchParams.get('cursor')).toBe('list:listBooks:abcd1234:1');

    const jobsUrl = new URL(jobsCall.url);
    expect(jobsUrl.pathname).toBe('/api/v1/projects/project_1/jobs');
    expect(jobsUrl.searchParams.get('kind')).toBe('canon_text_index');
    expect(jobsUrl.searchParams.get('limit')).toBe('2');

    const chaptersUrl = new URL(chaptersCall.url);
    expect(chaptersUrl.pathname).toBe('/api/v1/books/book_1/chapters');
    expect(chaptersUrl.searchParams.get('limit')).toBe('3');

    expect(csrfCall.url).toBe('https://api.example.test/api/v1/auth/csrf');
    expect(searchCall.method).toBe('POST');
    expect(searchCall.url).toBe('https://api.example.test/api/v1/projects/project_1/search');
    expect(searchCall.headers.get('X-CSRF-Token')).toBe('csrf_test_token_12345');
    await expect(searchCall.clone().json()).resolves.toMatchObject({ query: 'port', scope: 'all', sourceView: 'published' });
  });

  it('narrows source list states and routes restore operations through generated paths', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return csrfResponse();
        }
        return jsonResponse({ data: [], meta: { hasMore: false, nextCursor: null } });
      },
    });

    const activeProjects = await api.listProjects();
    const deletedProjects = await api.listProjects({ state: 'deleted' });
    const activeBooks = await api.listBooks('project_1');
    const deletedBooks = await api.listBooks('project_1', { state: 'deleted' });
    const activeChapters = await api.listChapters('book_1');
    const deletedChapters = await api.listChapters('book_1', { state: 'deleted' });
    await api.restoreProject('project_1');
    await api.restoreBook('book_1');
    await api.restoreChapter('chapter_1');

    const activeProjectBookCount: number | undefined = activeProjects.data[0]?.bookCount;
    const deletedProjectRestoreUntil: string | undefined = deletedProjects.data[0]?.restoreUntil;
    const activeBookProjectId: string | undefined = activeBooks.data[0]?.projectId;
    const deletedBookRestoreUntil: string | undefined = deletedBooks.data[0]?.restoreUntil;
    const activeChapterOrder: number | undefined = activeChapters.data[0]?.order;
    const deletedChapterRestoreUntil: string | undefined = deletedChapters.data[0]?.restoreUntil;
    void [
      activeProjectBookCount,
      deletedProjectRestoreUntil,
      activeBookProjectId,
      deletedBookRestoreUntil,
      activeChapterOrder,
      deletedChapterRestoreUntil,
    ];

    const operationCalls = calls.filter((request) => !request.url.endsWith('/auth/csrf'));
    expect(operationCalls.map((request) => `${request.method} ${new URL(request.url).pathname}`)).toEqual([
      'GET /api/v1/projects',
      'GET /api/v1/projects',
      'GET /api/v1/projects/project_1/books',
      'GET /api/v1/projects/project_1/books',
      'GET /api/v1/books/book_1/chapters',
      'GET /api/v1/books/book_1/chapters',
      'POST /api/v1/projects/project_1/restore',
      'POST /api/v1/books/book_1/restore',
      'POST /api/v1/chapters/chapter_1/restore',
    ]);
    for (const index of [1, 3, 5]) {
      expect(new URL(operationCalls[index]!.url).searchParams.get('state')).toBe('deleted');
    }
    for (const request of operationCalls.slice(6)) {
      expect(request.headers.get('X-CSRF-Token')).toBe('csrf_test_token_12345');
    }
  });

  it('maps OpenAPI problem responses separately from network failures', async () => {
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => jsonResponse({ type: 'about:blank', title: 'Forbidden', status: 403 }, 403),
    });

    await expect(api.listProjects()).rejects.toMatchObject({
      name: 'ApiStatusError',
      status: 403,
      problem: { title: 'Forbidden', status: 403 },
    });
  });

  it('maps thrown fetch failures to NetworkApiError', async () => {
    const api = createCanonKeeperApiClient({
      fetch: async () => {
        throw new TypeError('Failed to fetch');
      },
    });

    await expect(api.listProjects()).rejects.toBeInstanceOf(NetworkApiError);
  });

  it('rejects missing idempotency keys before idempotent helper requests reach fetch', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        return jsonResponse({}, 500);
      },
    });

    expect(() => api.startProjectIndexing('project_1', { sourceView: 'published' }, undefined as never)).toThrow(
      'startProjectIndexing requires an Idempotency-Key.',
    );
    await expect(
      api.importBookFile('project_1', { file: new File(['chapter'], 'draft.epub') }, undefined as never),
    ).rejects.toThrow('importBookFile requires an Idempotency-Key.');
    expect(calls).toHaveLength(0);
  });

  it('keeps operation manifest paths mechanically checked against generated operations', () => {
    expect(operationManifest.loginUser).toMatchObject({ method: 'POST', path: '/auth/login' });
    expect(operationManifest.getCurrentUser).toMatchObject({ method: 'GET', path: '/auth/session' });
    expect(operationManifest.searchProject).toMatchObject({ method: 'POST', path: '/projects/{projectId}/search' });
    expect(operationManifest.getIndexingStatus).toMatchObject({ method: 'GET', path: '/projects/{projectId}/indexing-status' });
    expect(operationManifest.startProjectIndexing).toMatchObject({ method: 'POST', path: '/projects/{projectId}/indexing-runs' });
    expect(Object.values(operationManifest).every((route) => route.method.length > 0 && route.path.startsWith('/'))).toBe(true);
  });
});
