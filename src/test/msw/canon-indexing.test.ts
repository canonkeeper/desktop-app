import { describe, expect, it } from 'vitest';
import { createApiHandlers, mswApiBaseUrl } from './handlers';
import { mswServer } from './server';

describe('MSW canonical indexing handlers', () => {
  it('keeps source-view gates and projections intact in the MSW adapter', async () => {
    mswServer.use(...createApiHandlers());
    const viewerHeaders = {
      cookie: 'ck_session=mock-session',
      mock_actor_role: 'viewer',
      origin: 'http://localhost:3000',
      'x-csrf-token': 'ck_msw_csrf_token_000000',
      'content-type': 'application/json',
    };

    const publishedSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: viewerHeaders,
      body: JSON.stringify({ query: 'порт', scope: 'all', sourceView: 'published' }),
    });
    expect(publishedSearch.status).toBe(200);
    await expect(publishedSearch.json()).resolves.toMatchObject({
      data: expect.arrayContaining([
        expect.objectContaining({ kind: 'chapter', locator: expect.objectContaining({ sourceView: 'published' }) }),
      ]),
      textCoverage: expect.objectContaining({ truncated: false }),
    });
    const draftSearch = await fetch(`${mswApiBaseUrl}/projects/project-white-port/search`, {
      method: 'POST',
      headers: viewerHeaders,
      body: JSON.stringify({ query: 'порт', scope: 'all', sourceView: 'draft' }),
    });
    expect(draftSearch.status).toBe(404);

    const viewerStatus = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-status`, { headers: viewerHeaders });
    const viewerStatusBody = (await viewerStatus.json()) as Record<string, unknown>;
    // The published text of the mock saga is read: a reader sees the reading too (specs/2026-11-10-ux3-saga-state.md SS1.1).
    expect(Object.keys(viewerStatusBody).sort()).toEqual(['preparation', 'published', 'steps']);

    const editorHeaders = { ...viewerHeaders, mock_actor_role: 'editor' };
    const editorStatus = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-status`, { headers: editorHeaders });
    const editorStatusBody = (await editorStatus.json()) as { draft?: { context?: Record<string, unknown> | null } };
    expect(Object.keys(editorStatusBody)).toContain('draft');
    expect(editorStatusBody.draft?.context).toHaveProperty('retryableFailedBook', { bookId: 'book-04' });
    const checklist = await fetch(
      `${mswApiBaseUrl}/projects/project-white-port/indexing-status?bookId=book-04&sourceView=draft`,
      { headers: editorHeaders },
    );
    const checklistBody = (await checklist.json()) as { steps: Array<{ kind: string }> };
    expect(checklistBody.steps.map((step) => step.kind)).toEqual(['import', 'text_index', 'cards', 'ready']);

    const draftIndex = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...editorHeaders, 'idempotency-key': 'idem-msw-draft-text-index' },
      body: JSON.stringify({ sourceView: 'draft' }),
    });
    expect(draftIndex.status).toBe(202);
    await expect(draftIndex.json()).resolves.toMatchObject({
      job: { kind: 'canon_text_index', subject: { type: 'canon_text_index', sourceView: 'draft' } },
    });
    const invalidProjectBook = await fetch(`${mswApiBaseUrl}/projects/project-white-port/indexing-runs`, {
      method: 'POST',
      headers: { ...editorHeaders, 'idempotency-key': 'idem-msw-invalid-project-book' },
      body: JSON.stringify({ scope: 'project', sourceView: 'draft', bookId: 'book-03' }),
    });
    expect(invalidProjectBook.status).toBe(400);
  });
});
