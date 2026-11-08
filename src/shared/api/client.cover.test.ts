import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError, NetworkApiError, createCanonKeeperApiClient } from './index';

type XhrScenario = {
  status: number;
  statusText: string;
  body: string;
  headers: Record<string, string>;
  progress?: Array<{ lengthComputable: boolean; loaded: number; total: number }>;
};

let scenarios: XhrScenario[] = [];
let requests: CoverXmlHttpRequest[] = [];

class CoverXmlHttpRequest {
  readonly headers = new Headers();
  readonly upload = { onprogress: null as ((event: ProgressEvent) => void) | null };
  body: XMLHttpRequestBodyInit | null = null;
  method = '';
  url = '';
  withCredentials = false;
  status = 0;
  statusText = '';
  responseText = '';
  responseHeaders: Record<string, string> = {};
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;

  constructor() {
    requests.push(this);
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  getAllResponseHeaders() {
    return Object.entries(this.responseHeaders).map(([name, value]) => `${name}: ${value}`).join('\r\n');
  }

  send(body: XMLHttpRequestBodyInit | null) {
    this.body = body;
    const scenario = scenarios.shift();
    if (!scenario) throw new Error('Missing XHR scenario.');
    queueMicrotask(() => {
      for (const progress of scenario.progress ?? []) {
        this.upload.onprogress?.(progress as ProgressEvent);
      }
      this.status = scenario.status;
      this.statusText = scenario.statusText;
      this.responseText = scenario.body;
      this.responseHeaders = scenario.headers;
      this.onload?.();
    });
  }

  abort() {
    this.onabort?.();
  }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
}

function book(coverUrl: string | null) {
  return {
    id: 'book-1', projectId: 'project-1', title: 'Book', subtitle: null, order: 1, displayNumber: 'I',
    coverColor: '#123456', coverUrl, status: 'ready', chapterCount: 1, wordCount: 2,
    createdAt: '2026-09-04T00:00:00.000Z', updatedAt: '2026-09-04T00:00:00.000Z',
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  scenarios = [];
  requests = [];
});

describe('book cover and Chat rename client', () => {
  it('uploads exact multipart with CSRF recovery and byte progress but no idempotency header', async () => {
    vi.stubGlobal('XMLHttpRequest', CoverXmlHttpRequest);
    scenarios = [
      {
        status: 403, statusText: 'Forbidden',
        body: JSON.stringify({ type: 'about:blank', title: 'Forbidden', status: 403, code: 'csrf.invalid', requestId: 'req-1' }),
        headers: { 'content-type': 'application/problem+json' },
      },
      {
        status: 200, statusText: 'OK', body: JSON.stringify(book('/api/v1/books/book-1/cover/1')),
        headers: { 'content-type': 'application/json' },
        progress: [{ lengthComputable: true, loaded: 7, total: 7 }],
      },
    ];
    let csrfVersion = 0;
    const csrfTokens = ['csrf_test_token_12345', 'ck_mock_csrf_token_000000'];
    const progress: unknown[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => jsonResponse({ csrfToken: csrfTokens[csrfVersion++], expiresAt: '2026-09-04T01:00:00.000Z', rotation: 'refreshed' }),
    });
    const file = new File(['cover!'], 'cover.jpg', { type: 'image/jpeg' });

    await expect(api.uploadBookCover('book-1', file, { onUploadProgress: (value) => progress.push(value) }))
      .resolves.toMatchObject({ coverUrl: '/api/v1/books/book-1/cover/1' });

    expect(requests).toHaveLength(2);
    expect(requests.map((request) => request.headers.get('X-CSRF-Token'))).toEqual(csrfTokens);
    expect(requests.every((request) => request.headers.get('Idempotency-Key') === null)).toBe(true);
    expect(requests[1]).toMatchObject({ method: 'POST', url: 'https://api.example.test/api/v1/books/book-1/cover', withCredentials: true });
    expect([...(requests[1]!.body as FormData).keys()]).toEqual(['file']);
    expect((requests[1]!.body as FormData).get('file')).toBe(file);
    expect(progress).toEqual([{ loadedBytes: 7, totalBytes: 7 }]);
  });

  it('maps malformed success and Problem responses without a second transport', async () => {
    vi.stubGlobal('XMLHttpRequest', CoverXmlHttpRequest);
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => jsonResponse({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-09-04T01:00:00.000Z', rotation: 'refreshed' }),
    });
    scenarios = [{ status: 200, statusText: 'OK', body: '{', headers: { 'content-type': 'application/json' } }];
    await expect(api.uploadBookCover('book-1', new File(['x'], 'cover.png'))).rejects.toBeInstanceOf(NetworkApiError);

    scenarios = [{
      status: 415, statusText: 'Unsupported Media Type',
      body: JSON.stringify({ type: 'about:blank', title: 'Unsupported', status: 415, code: 'cover.unsupported_media', requestId: 'req-2' }),
      headers: { 'content-type': 'application/problem+json' },
    }];
    await expect(api.uploadBookCover('book-1', new File(['x'], 'cover.gif'))).rejects.toMatchObject({
      constructor: ApiStatusError,
      status: 415,
      problem: { code: 'cover.unsupported_media' },
    });
  });

  it('renames and deletes through generated JSON operations', async () => {
    const calls: Request[] = [];
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async (request) => {
        calls.push(request);
        if (request.url.endsWith('/auth/csrf')) {
          return jsonResponse({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-09-04T01:00:00.000Z', rotation: 'refreshed' });
        }
        return jsonResponse(book(request.method === 'DELETE' ? null : '/api/v1/books/book-1/cover/1'));
      },
    });

    await api.renameChatSession('chat-1', { title: 'Новое название' });
    await api.deleteBookCover('book-1');

    const patch = calls.find((request) => request.method === 'PATCH')!;
    expect(patch.url).toBe('https://api.example.test/api/v1/chats/chat-1');
    await expect(patch.clone().json()).resolves.toEqual({ title: 'Новое название' });
    expect(calls.find((request) => request.method === 'DELETE')?.url).toBe('https://api.example.test/api/v1/books/book-1/cover');
  });
});
