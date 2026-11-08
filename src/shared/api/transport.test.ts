import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NetworkApiError, createCanonKeeperApiClient } from './index';

type UploadEvent = Pick<ProgressEvent, 'lengthComputable' | 'loaded' | 'total'>;
type LoadScenario = {
  type: 'load';
  status: number;
  statusText: string;
  body: string;
  headers?: Record<string, string>;
  progress?: UploadEvent[];
};
type XhrScenario = LoadScenario | { type: 'error' } | { type: 'pending' };

let scenario: XhrScenario;
let requests: FakeXmlHttpRequest[];
let onSend: ((request: FakeXmlHttpRequest) => void) | undefined;

class FakeXmlHttpRequest {
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
  aborted = false;
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
    return Object.entries(this.responseHeaders)
      .map(([name, value]) => `${name}: ${value}`)
      .join('\r\n');
  }

  send(body: XMLHttpRequestBodyInit | null) {
    this.body = body;
    onSend?.(this);
    const response = scenario;
    if (response.type === 'pending') return;

    queueMicrotask(() => {
      if (response.type === 'error') {
        this.onerror?.();
        return;
      }
      for (const progress of response.progress ?? []) {
        this.upload.onprogress?.(new ProgressEvent('progress', progress));
      }
      this.status = response.status;
      this.statusText = response.statusText;
      this.responseText = response.body;
      this.responseHeaders = response.headers ?? {};
      this.onload?.();
    });
  }

  abort() {
    this.aborted = true;
    this.onabort?.();
  }
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    headers: { 'content-type': 'application/json' },
  });
}

function createApi() {
  const fetch = vi.fn(async () =>
    jsonResponse({
      csrfToken: 'csrf_test_token_12345',
      expiresAt: '2026-06-16T05:10:00.000Z',
      rotation: 'refreshed',
    }),
  );
  return {
    api: createCanonKeeperApiClient({ baseUrl: 'https://api.example.test/api/v1', fetch }),
    fetch,
  };
}

function importDraft(
  api: ReturnType<typeof createCanonKeeperApiClient>,
  options: Parameters<typeof api.importBookFile>[2],
) {
  return api.importBookFile(
    'project_1',
    {
      file: new File(['chapter'], 'draft.epub', { type: 'application/epub+zip' }),
      metadata: { title: 'Draft', sourceFileName: 'draft.epub' },
      options: { importMode: 'new_book' },
    },
    options,
  );
}

function acceptedScenario(progress?: UploadEvent[]): LoadScenario {
  const job = {
    id: 'import-job-1',
    kind: 'import',
    status: 'queued',
    progress: 0,
    phase: null,
    subject: { type: 'import', id: 'project_1', projectId: 'project_1' },
    result: null,
    error: null,
    canCancel: true,
    expiresAt: null,
    createdAt: '2026-08-14T10:00:00.000Z',
    updatedAt: '2026-08-14T10:00:00.000Z',
    links: {
      self: '/api/v1/jobs/import-job-1',
      cancel: '/api/v1/jobs/import-job-1/cancel',
    },
  };
  return {
    type: 'load',
    status: 202,
    statusText: 'Accepted',
    body: JSON.stringify({ jobId: job.id, job, links: { job: '/api/v1/jobs/import-job-1' } }),
    headers: { 'content-type': 'application/json' },
    progress,
  };
}

beforeEach(() => {
  requests = [];
  onSend = undefined;
  scenario = acceptedScenario();
  vi.stubGlobal('XMLHttpRequest', FakeXmlHttpRequest);
});

afterEach(() => vi.unstubAllGlobals());

describe('multipart import transport', () => {
  it('reacquires CSRF before retrying a rejected multipart request', async () => {
    const csrfValues = ['csrf_test_token_12345', 'ck_csrf_example_token_000000'] as const;
    let csrfVersion = 0;
    const fetch = vi.fn(async () => jsonResponse({
      csrfToken: csrfValues[csrfVersion++],
      expiresAt: '2026-09-01T20:00:00.000Z',
      rotation: 'refreshed',
    }));
    const api = createCanonKeeperApiClient({ baseUrl: 'https://api.example.test/api/v1', fetch });
    let uploadAttempt = 0;
    onSend = () => {
      uploadAttempt += 1;
      scenario = uploadAttempt === 1
        ? {
            type: 'load',
            status: 403,
            statusText: 'Forbidden',
            body: JSON.stringify({
              type: 'about:blank',
              title: 'Forbidden',
              status: 403,
              code: 'csrf.invalid',
              requestId: 'req_stale_upload_csrf',
            }),
            headers: { 'content-type': 'application/problem+json' },
          }
        : acceptedScenario();
    };

    await expect(importDraft(api, { idempotencyKey: 'idem-stale-upload' }))
      .resolves.toMatchObject({ jobId: 'import-job-1' });

    expect(fetch).toHaveBeenCalledTimes(2);
    expect(requests).toHaveLength(2);
    expect(requests[0]?.headers.get('X-CSRF-Token')).toBe(csrfValues[0]);
    expect(requests[1]?.headers.get('X-CSRF-Token')).toBe(csrfValues[1]);
    expect(requests[0]?.headers.get('Idempotency-Key')).toBe('idem-stale-upload');
    expect(requests[1]?.headers.get('Idempotency-Key')).toBe('idem-stale-upload');
  });

  it('sends native FormData with credentials, contract headers and honest upload progress', async () => {
    scenario = acceptedScenario([
      { lengthComputable: false, loaded: 1, total: 0 },
      { lengthComputable: true, loaded: 25, total: 100 },
    ]);
    const progress: Array<{ loadedBytes: number; totalBytes: number | null }> = [];
    const { api, fetch } = createApi();

    const started = await importDraft(api, {
      idempotencyKey: 'idem-import-fixed',
      onUploadProgress: (value) => progress.push(value),
    });

    expect(started.links).toEqual({ job: '/api/v1/jobs/import-job-1' });
    expect(progress).toEqual([
      { loadedBytes: 1, totalBytes: null },
      { loadedBytes: 25, totalBytes: 100 },
    ]);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(requests).toHaveLength(1);
    const request = requests[0]!;
    expect([request.method, request.url, request.withCredentials]).toEqual([
      'POST',
      'https://api.example.test/api/v1/projects/project_1/imports',
      true,
    ]);
    expect(request.headers.get('X-CSRF-Token')).toBe('csrf_test_token_12345');
    expect(request.headers.get('Idempotency-Key')).toBe('idem-import-fixed');
    expect(request.headers.has('content-type')).toBe(false);
    expect(request.body).toBeInstanceOf(FormData);
    const form = request.body as FormData;
    expect((form.get('file') as File).name).toBe('draft.epub');
    expect(form.get('metadata')).toBe(JSON.stringify({ title: 'Draft', sourceFileName: 'draft.epub' }));
    expect(form.get('options')).toBe(JSON.stringify({ importMode: 'new_book' }));
  });

  it('keeps a JSON Problem response as a typed status error', async () => {
    scenario = {
      type: 'load',
      status: 422,
      statusText: 'Unprocessable Entity',
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Unsupported manuscript',
        status: 422,
        code: 'import.unsupported',
        requestId: 'req_test',
      }),
      headers: { 'content-type': 'application/problem+json' },
    };

    await expect(importDraft(createApi().api, { idempotencyKey: 'idem-json-problem' })).rejects.toMatchObject({
      name: 'ApiStatusError',
      status: 422,
      problem: { title: 'Unsupported manuscript', code: 'import.unsupported' },
    });
  });

  it('maps a proxy HTML 413 to the same safe typed status error', async () => {
    scenario = {
      type: 'load',
      status: 413,
      statusText: 'Payload Too Large',
      body: '<html><body>nginx</body></html>',
      headers: { 'content-type': 'text/html' },
    };

    await expect(importDraft(createApi().api, { idempotencyKey: 'idem-html-413' })).rejects.toMatchObject({
      name: 'ApiStatusError',
      status: 413,
      problem: { title: 'Payload Too Large', code: 'request_failed' },
    });
  });

  it('maps a network failure to NetworkApiError', async () => {
    scenario = { type: 'error' };

    await expect(importDraft(createApi().api, { idempotencyKey: 'idem-network' })).rejects.toBeInstanceOf(NetworkApiError);
  });

  it('aborts the active native request through the supplied signal', async () => {
    scenario = { type: 'pending' };
    const sent = new Promise<void>((resolve) => {
      onSend = () => resolve();
    });
    const controller = new AbortController();
    const request = importDraft(createApi().api, {
      idempotencyKey: 'idem-abort',
      signal: controller.signal,
    });
    const rejection = expect(request).rejects.toMatchObject({
      name: 'NetworkApiError',
      cause: { name: 'AbortError' },
    });

    await sent;
    controller.abort();

    await rejection;
    expect(requests[0]?.aborted).toBe(true);
  });
});
