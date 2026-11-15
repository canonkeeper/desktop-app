import { afterEach, describe, expect, it, vi } from 'vitest';
import { NetworkApiError, createCanonKeeperApiClient } from './index';

// specs/2026-11-13-desk-sagafile-web.md SW1.3, SW2.2, SW3: the saga file of the pages by the operations of the server
// (specs/2026-11-13-desk-sagafile.md SF2, SF3, SF3.8).

afterEach(() => vi.unstubAllGlobals());

describe('getSagaArchive', () => {
  it('takes the file and the name the server gave it', async () => {
    const fetch = vi.fn(async (_request: Request) => new Response('PK', {
      headers: { 'Content-Disposition': `attachment; filename*=utf-8''${encodeURIComponent('Хроники Белого порта.cksaga')}` },
    }));
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });

    const file = await client.getSagaArchive('project-1');

    expect(file.fileName).toBe('Хроники Белого порта.cksaga');
    expect(await file.blob.text()).toBe('PK');
    expect(fetch.mock.calls[0]![0].url).toBe('http://test.local/api/v1/projects/project-1/saga-archive');
  });

  // SW1.2: «Остановить» breaks the request off — the file is not saved, the request does not go on.
  it('breaks off with the signal of the page', async () => {
    const fetch = vi.fn((request: Request) => new Promise<Response>((_resolve, reject) => {
      request.signal.addEventListener('abort', () => reject(new DOMException('The page stopped the file.', 'AbortError')));
    }));
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });
    const controller = new AbortController();

    const saving = client.getSagaArchive('project-1', { signal: controller.signal });
    controller.abort();

    await expect(saving).rejects.toBeInstanceOf(NetworkApiError);
    expect(fetch.mock.calls[0]![0].signal.aborted).toBe(true);
  });
});

describe('openSagaArchive', () => {
  it('sends the file with its key of a repeat and reads the saga it opened', async () => {
    const sent: { body: XMLHttpRequestBodyInit | null; headers: Record<string, string>; url: string } = { body: null, headers: {}, url: '' };
    class OpenRequest {
      readonly upload = { onprogress: null };
      status = 201;
      statusText = 'Created';
      responseText = JSON.stringify({ projectId: 'project-9', name: 'Северный предел', renamed: false, report: {} });
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      onabort: (() => void) | null = null;
      withCredentials = false;
      open(_method: string, url: string) { sent.url = url; }
      setRequestHeader(name: string, value: string) { sent.headers[name] = value; }
      getAllResponseHeaders() { return 'content-type: application/json'; }
      send(body: XMLHttpRequestBodyInit | null) {
        sent.body = body;
        queueMicrotask(() => this.onload?.());
      }
      abort() { this.onabort?.(); }
    }
    vi.stubGlobal('XMLHttpRequest', OpenRequest);
    const client = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => Response.json({ csrfToken: 'csrf_test_token_12345', expiresAt: '2026-11-13T10:00:00.000Z', rotation: 'refreshed' }),
    });

    const opened = await client.openSagaArchive(new File(['PK'], 'Северный предел.cksaga'), { idempotencyKey: 'idem-open-saga-file-1' });

    expect(opened.projectId).toBe('project-9');
    expect(sent.url).toBe('https://api.example.test/api/v1/saga-archives');
    expect(sent.headers['Idempotency-Key']).toBe('idem-open-saga-file-1');
    expect((sent.body as FormData).get('file')).toMatchObject({ name: 'Северный предел.cksaga' });
  });

  it('never goes without a key of a repeat', async () => {
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch: vi.fn() });
    const none = { idempotencyKey: '' };
    await expect(client.openSagaArchive(new File(['PK'], 'a.cksaga'), none)).rejects.toThrow('openSagaArchive requires an Idempotency-Key.');
  });
});

describe('the chats of a saga file', () => {
  it('lists them and reads one', async () => {
    const fetch = vi.fn(async (request: Request) => Response.json(request.url.endsWith('/transcripts') ? { data: [] } : { id: 't-1' }));
    const client = createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch });

    expect(await client.listSagaTranscripts('project-1')).toEqual({ data: [] });
    expect(await client.getSagaTranscript('project-1', 't-1')).toEqual({ id: 't-1' });
    expect(fetch.mock.calls.map(([request]) => request.url)).toEqual([
      'http://test.local/api/v1/projects/project-1/transcripts',
      'http://test.local/api/v1/projects/project-1/transcripts/t-1',
    ]);
  });
});
