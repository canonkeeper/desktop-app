import { afterEach, describe, expect, it, vi } from 'vitest';
import { NetworkApiError, createCanonKeeperApiClient } from './index';

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function csrfResponse() {
  return jsonResponse({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-06-16T05:10:00.000Z', rotation: 'refreshed' });
}

class MultipartRequest {
  readonly upload = { onprogress: null as ((event: ProgressEvent) => void) | null };
  body: XMLHttpRequestBodyInit | null = null;
  status = 202;
  statusText = 'Accepted';
  responseText = JSON.stringify({
    jobId: 'import-job-1',
    job: { id: 'import-job-1' },
    links: { job: '/jobs/import-job-1' },
  });
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  withCredentials = false;

  open() {}
  setRequestHeader() {}
  getAllResponseHeaders() { return 'content-type: application/json'; }
  send(body: XMLHttpRequestBodyInit | null) {
    this.body = body;
    multipartBody = body;
    queueMicrotask(() => this.onload?.());
  }
  abort() { this.onabort?.(); }
}

let multipartBody: XMLHttpRequestBodyInit | null = null;

afterEach(() => vi.unstubAllGlobals());

describe('OpenApiCanonKeeperClient', () => {
  it('uploads a durable queue item as multipart with its stable item identity', async () => {
    class QueueItemMultipartRequest extends MultipartRequest {
      status = 200;
      statusText = 'OK';
      responseText = JSON.stringify({
        id: 'queue-1',
        projectId: 'project_1',
        status: 'uploading',
        version: 2,
        intendedItemCount: 1,
        nextBookOrder: 1,
        items: [],
        createdAt: '2026-08-20T10:00:00.000Z',
        updatedAt: '2026-08-20T10:00:01.000Z',
        startedAt: null,
        completedAt: null,
        links: { self: '/projects/project_1/import-queues/queue-1', cancel: null },
      });
    }
    vi.stubGlobal('XMLHttpRequest', QueueItemMultipartRequest);
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await api.uploadImportQueueItem(
      'project_1',
      'queue-1',
      { itemId: 'item-1', file: new File(['<FictionBook/>'], 'volume-1.fb2') },
      { idempotencyKey: 'idem-queue-item-upload-1' },
    );

    expect((multipartBody as FormData).get('itemId')).toBe('item-1');
    expect((multipartBody as FormData).get('file')).toMatchObject({
      name: 'volume-1.fb2',
      type: 'application/x-fictionbook+xml',
    });
  });

  it('requires an idempotency key before starting a durable queue', async () => {
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    expect(() => api.startImportQueue(
      'project_1',
      'queue-1',
      { expectedVersion: 4 },
      undefined as never,
    )).toThrow('startImportQueue requires an Idempotency-Key.');
    await expect(api.uploadImportQueueItem(
      'project_1',
      'queue-1',
      { itemId: 'item-1', file: new File(['chapter'], 'volume.fb2') },
      undefined as never,
    )).rejects.toThrow('uploadImportQueueItem requires an Idempotency-Key.');
  });

  it('normalizes an extension-valid FB2 file with an empty browser MIME type for multipart import', async () => {
    vi.stubGlobal('XMLHttpRequest', MultipartRequest);
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await api.importBookFile(
      'project_1',
      { file: new File(['<FictionBook/>'], 'story.fb2') },
      { idempotencyKey: 'idem-fb2-mime' },
    );

    expect((multipartBody as FormData).get('file')).toMatchObject({
      name: 'story.fb2',
      type: 'application/x-fictionbook+xml',
    });
  });

  // specs/2026-11-10-book-file-kinds.md K1.2: every kind goes with the media type of the end of its name.
  it.each([
    ['rukopis.txt', 'text/plain'],
    ['glava.MD', 'text/markdown'],
    ['roman.rtf', 'application/rtf'],
    ['roman.odt', 'application/vnd.oasis.opendocument.text'],
    ['Том 2.fb2.zip', 'application/zip'],
  ])('sends %s with the media type of its kind', async (name, type) => {
    vi.stubGlobal('XMLHttpRequest', MultipartRequest);
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await api.importBookFile('project_1', { file: new File(['book'], name, { type: 'application/octet-stream' }) }, { idempotencyKey: `idem-kind-${type}` });

    expect((multipartBody as FormData).get('file')).toMatchObject({ name, type });
  });

  it('treats a successful import response without usable job identity as an unknown outcome', async () => {
    class MalformedMultipartRequest extends MultipartRequest {
      responseText = '{';
    }
    vi.stubGlobal('XMLHttpRequest', MalformedMultipartRequest);
    const malformed = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await expect(malformed.importBookFile(
      'project_1',
      { file: new File(['chapter'], 'draft.epub') },
      { idempotencyKey: 'idem-malformed-import' },
    )).rejects.toBeInstanceOf(NetworkApiError);

    class MissingJobIdRequest extends MultipartRequest {
      responseText = JSON.stringify({ jobId: 'missing-job-id', job: { id: ' ' }, links: {} });
    }
    vi.stubGlobal('XMLHttpRequest', MissingJobIdRequest);
    const missingJobId = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await expect(missingJobId.importBookFile(
      'project_1',
      { file: new File(['chapter'], 'draft.epub') },
      { idempotencyKey: 'idem-missing-job-id' },
    )).rejects.toBeInstanceOf(NetworkApiError);
  });

  it('treats a successful JSON null import response as an unknown outcome', async () => {
    class NullResponseRequest extends MultipartRequest {
      responseText = 'null';
    }
    vi.stubGlobal('XMLHttpRequest', NullResponseRequest);
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => csrfResponse(),
    });

    await expect(api.importBookFile(
      'project_1',
      { file: new File(['chapter'], 'draft.epub') },
      { idempotencyKey: 'idem-null-import-response' },
    )).rejects.toBeInstanceOf(NetworkApiError);
  });
});
