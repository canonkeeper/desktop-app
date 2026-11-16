import { afterEach, describe, expect, it, vi } from 'vitest';
import { createCanonKeeperApiClient } from './index';

// specs/2026-11-15-desk-obsidian-web.md OW7.1: nine explicit methods of the bridge with an Obsidian vault; the keys of a
// repeat go in their header; the reads under the fence of the saga take no signal and the client keeps no term of its own.

afterEach(() => vi.unstubAllGlobals());

const csrf = { csrfToken: 'csrf_test_token_12345', expiresAt: '2026-11-15T10:00:00.000Z', rotation: 'refreshed' };

function recordingClient(answer: (request: Request) => Response = () => Response.json({})) {
  const requests: Request[] = [];
  const fetch = vi.fn(async (request: Request) => {
    if (request.url.endsWith('/auth/csrf')) return Response.json(csrf);
    requests.push(request);
    return answer(request);
  });
  return { client: createCanonKeeperApiClient({ baseUrl: 'http://test.local/api/v1', fetch }), requests };
}

describe('the client of the bridge with an Obsidian vault', () => {
  it('calls each operation by its method and path', async () => {
    const { client, requests } = recordingClient((request) => (request.method === 'DELETE' || request.method === 'PUT'
      ? new Response(null, { status: 204 }) : Response.json({ linked: false, choosing: false })));
    await client.getObsidianVault('saga-1');
    await client.beginObsidianVaultChoice('saga-1');
    await client.unlinkObsidianVault('saga-1');
    await client.scanObsidianVault('saga-1');
    await client.previewObsidianImport('saga-1', { books: ['Книги/Книга 1'] });
    await client.importObsidianMaterials('saga-1', { materials: [{ path: 'Герои', fingerprint: 'a'.repeat(64) }] }, { idempotencyKey: 'idem-notes-0001' });
    await client.importObsidianBooks('saga-1', { books: [{ folder: 'Книги/Книга 1', fingerprint: 'b'.repeat(64) }] }, { idempotencyKey: 'idem-books-0001' });
    await client.linkBookToObsidianFolder('saga-1', 'book-1', { folder: 'Книги/Книга 1' });
    await client.unlinkBookFromObsidianFolder('saga-1', 'book-1');
    expect(requests.map((request) => `${request.method} ${new URL(request.url).pathname}`)).toEqual([
      'GET /api/v1/projects/saga-1/obsidian',
      'POST /api/v1/projects/saga-1/obsidian/choice',
      'DELETE /api/v1/projects/saga-1/obsidian',
      'GET /api/v1/projects/saga-1/obsidian/tree',
      'POST /api/v1/projects/saga-1/obsidian/preview',
      'POST /api/v1/projects/saga-1/obsidian/materials',
      'POST /api/v1/projects/saga-1/obsidian/books',
      'PUT /api/v1/projects/saga-1/obsidian/books/book-1',
      'DELETE /api/v1/projects/saga-1/obsidian/books/book-1',
    ]);
    expect(requests[5]!.headers.get('Idempotency-Key')).toBe('idem-notes-0001');
    expect(requests[6]!.headers.get('Idempotency-Key')).toBe('idem-books-0001');
    expect(await requests[4]!.json()).toEqual({ books: ['Книги/Книга 1'] });
  });

  it('reads the tree and the preview without a signal the page could break them off with', async () => {
    const { client, requests } = recordingClient(() => Response.json({ folders: [], truncated: false, entries: 0, skipped: [] }));
    await client.scanObsidianVault('saga-1');
    await client.previewObsidianImport('saga-1', { books: ['Книги/Книга 1'] });
    // The two reads have no parameter for a signal at all: the type says so, and no request carries one that was aborted.
    expect(client.scanObsidianVault.length).toBe(1);
    expect(client.previewObsidianImport.length).toBe(2);
    expect(requests.every((request) => !request.signal.aborted)).toBe(true);
  });

  it('never writes without a key of a repeat', () => {
    const { client } = recordingClient();
    const none = { idempotencyKey: '' };
    expect(() => client.importObsidianBooks('saga-1', { books: [] }, none)).toThrow('importObsidianBooks requires an Idempotency-Key.');
    expect(() => client.importObsidianMaterials('saga-1', { materials: [] }, none)).toThrow('importObsidianMaterials requires an Idempotency-Key.');
  });

  // specs/2026-11-15-desk-obsidian-book-web.md OU2.2: «Обновить из Obsidian» — its body and the key of the window in its header.
  it('updates a book from its folder with the key of its write in its header', async () => {
    const start = { jobId: 'job-1', job: { id: 'job-1' }, links: { job: '/api/v1/jobs/job-1' } };
    const { client, requests } = recordingClient(() => Response.json(start, { status: 202 }));
    await client.updateBookFromObsidian('saga-1', 'book-1', { mode: 'preview' }, { idempotencyKey: 'idem-update-0001' });
    await client.updateBookFromObsidian('saga-1', 'book-1', { mode: 'apply', expectedFingerprint: 'c'.repeat(64), confirmUnread: true }, { idempotencyKey: 'idem-update-0002' });
    expect(requests.map((request) => `${request.method} ${new URL(request.url).pathname} ${request.headers.get('Idempotency-Key')}`)).toEqual([
      'POST /api/v1/projects/saga-1/obsidian/books/book-1/update idem-update-0001',
      'POST /api/v1/projects/saga-1/obsidian/books/book-1/update idem-update-0002',
    ]);
    expect(await requests[1]!.json()).toEqual({ mode: 'apply', expectedFingerprint: 'c'.repeat(64), confirmUnread: true });
    const none = { idempotencyKey: '' };
    expect(() => client.updateBookFromObsidian('saga-1', 'book-1', { mode: 'preview' }, none)).toThrow('updateBookFromObsidian requires an Idempotency-Key.');
  });
});

// specs/2026-11-15-desk-obsidian-export.md OE1.1: the export — the word of the author in its body, the key in its header, never
// without a key.
describe('the client of the export to Obsidian', () => {
  it('exports with the word of the author and the key of its write', async () => {
    const { client, requests } = recordingClient();
    await client.exportObsidianSaga('saga-1', { replaceChanged: true }, { idempotencyKey: 'idem-export-0001' });
    expect(`${requests[0]!.method} ${new URL(requests[0]!.url).pathname}`).toBe('POST /api/v1/projects/saga-1/obsidian/export');
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe('idem-export-0001');
    expect(await requests[0]!.json()).toEqual({ replaceChanged: true });
    const none = { idempotencyKey: '' };
    expect(() => client.exportObsidianSaga('saga-1', {}, none)).toThrow('exportObsidianSaga requires an Idempotency-Key.');
  });
});
