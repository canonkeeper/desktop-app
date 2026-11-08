import { describe, expect, it } from 'vitest';
import { createMockApiClient, type MockRepository } from './index';

const projectId = 'project-white-port';

/** Имитация работника: первое чтение запускает Job индекса или карточек, второе завершает его. */
async function finishJob(api: MockRepository, jobId: string) {
  await api.getJob(jobId);
  return api.getJob(jobId);
}

describe('mock API canonical indexing', () => {
  it('filters and paginates every current job kind', async () => {
    const api = createMockApiClient();

    await expect(api.listProjectJobs(projectId, { limit: 1 })).resolves.toEqual({
      data: [expect.objectContaining({ id: 'canon-text-index-job-1', kind: 'canon_text_index' })],
      meta: { hasMore: false, nextCursor: null },
    });
    for (const kind of [
      'import',
      'canon_text_index',
      'chapter_cards',
      'research_chat_turn',
    ] as const) {
      const page = await api.listProjectJobs(projectId, { kind, limit: 1 });
      expect(page.data.every((job) => job.kind === kind)).toBe(true);
      if (kind !== 'canon_text_index') {
        expect(page).toEqual({ data: [], meta: { hasMore: false, nextCursor: null } });
      }
    }
    await expect(api.listProjectJobs(projectId, { kind: 'canon_book_enrichment' as never }))
      .rejects.toMatchObject({ operationId: 'listProjectJobs', status: 400 });

    const newestIndexing = await api.startProjectIndexing(
      projectId,
      { sourceView: 'published' },
      { idempotencyKey: 'idem-direct-jobs-index' },
    );

    const firstIndexingPage = await api.listProjectJobs(projectId, { kind: 'canon_text_index', limit: 1 });
    expect(firstIndexingPage.data).toHaveLength(1);
    expect(firstIndexingPage.data[0]?.id).toBe(newestIndexing.jobId);
    expect(firstIndexingPage.data.every((job) => job.kind === 'canon_text_index')).toBe(true);
    expect(firstIndexingPage.meta.hasMore).toBe(true);
    if (!firstIndexingPage.meta.nextCursor) {
      throw new Error('Expected indexing jobs to return a next cursor.');
    }

    const secondIndexingPage = await api.listProjectJobs(projectId, {
      cursor: firstIndexingPage.meta.nextCursor,
      kind: 'canon_text_index',
      limit: 1,
    });
    expect(secondIndexingPage.data).toHaveLength(1);
    expect(secondIndexingPage.data[0]?.id).toBe('canon-text-index-job-1');
    expect(secondIndexingPage.data.every((job) => job.kind === 'canon_text_index')).toBe(true);
    expect(secondIndexingPage.meta.hasMore).toBe(false);

    await expect(api.listProjectJobs(projectId, {
      cursor: firstIndexingPage.meta.nextCursor,
      kind: 'import',
      limit: 1,
    })).rejects.toMatchObject({ operationId: 'listProjectJobs', status: 400 });
  });

  it('restarts the text index of the exact source view only', async () => {
    const api = createMockApiClient();

    const publishedRun = await api.startProjectIndexing(
      projectId,
      { sourceView: 'published' },
      { idempotencyKey: 'idem-direct-published-text' },
    );
    expect(publishedRun.job).toMatchObject({
      kind: 'canon_text_index',
      status: 'queued',
      canCancel: true,
      subject: { type: 'canon_text_index', projectId, sourceView: 'published' },
    });

    const draftRun = await api.startProjectIndexing(
      projectId,
      { sourceView: 'draft' },
      { idempotencyKey: 'idem-direct-draft-text' },
    );
    expect(draftRun.job).toMatchObject({ kind: 'canon_text_index', subject: { sourceView: 'draft' } });
    expect(draftRun.jobId).not.toBe(publishedRun.jobId);

    for (const [body, idempotencyKey] of [
      [{ scope: 'project', sourceView: 'published' }, 'idem-direct-legacy-scope'],
      [{ sourceView: 'draft', bookId: 'book-03' }, 'idem-direct-legacy-book'],
      [{}, 'idem-direct-missing-source-view'],
    ] as const) {
      await expect(api.startProjectIndexing(projectId, body as never, { idempotencyKey }))
        .rejects.toMatchObject({ operationId: 'startProjectIndexing', status: 400 });
    }
    api.setScenario({ preset: 'normal', actorRole: 'viewer' });
    await expect(api.startProjectIndexing(projectId, { sourceView: 'published' }, { idempotencyKey: 'idem-direct-viewer-text' }))
      .rejects.toMatchObject({ operationId: 'startProjectIndexing', status: 403 });
  });

  it('builds chapter cards of the same source view after its text index', async () => {
    const api = createMockApiClient();

    const initial = await api.getIndexingStatus(projectId);
    expect(initial.steps).toEqual([]);
    expect(initial.published).toEqual({
      exists: true,
      context: { state: 'processing', currentBook: { bookId: 'book-02', position: 2, total: 2 }, retryableFailedBook: null },
    });
    const scope = { bookId: 'book-02', sourceView: 'published' } as const;
    const waiting = await api.getIndexingStatus(projectId, scope);
    expect(waiting.steps.map((step) => step.kind)).toEqual(['import', 'text_index', 'cards', 'ready']);
    expect(waiting.steps.slice(1)).toEqual([
      expect.objectContaining({
        kind: 'text_index', status: 'succeeded', progress: 1, sourceView: 'published',
        jobId: 'canon-text-index-job-1', counters: { indexedChapters: 1, totalChapters: 1 },
      }),
      expect.objectContaining({ kind: 'cards', status: 'pending', progress: 0, counters: { indexedChapters: 0, totalChapters: 1 } }),
      { kind: 'ready', status: 'pending', progress: null, bookId: 'book-02', sourceView: 'published' },
    ]);

    await expect(api.getJob('canon-text-index-job-1')).resolves.toMatchObject({ status: 'running', result: null });
    await expect(api.getJob('canon-text-index-job-1')).resolves.toMatchObject({
      status: 'succeeded',
      progress: 1,
      result: {
        type: 'canon_text_index',
        sourceView: 'published',
        textCoverage: { currentBooks: 2, totalBooks: 2, nonCurrentBooks: 0, totalNonCurrentBooks: 0, truncated: false },
      },
    });
    const cardsPage = await api.listProjectJobs(projectId, { kind: 'chapter_cards' });
    expect(cardsPage.data).toEqual([expect.objectContaining({
      kind: 'chapter_cards',
      status: 'queued',
      subject: { type: 'chapter_cards', projectId, sourceView: 'published' },
    })]);
    const cardsJobId = cardsPage.data[0]!.id;
    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    await expect(api.getJob(cardsJobId)).resolves.toMatchObject({ status: 'running', canCancel: false });
    expect((await api.getIndexingStatus(projectId)).published).toMatchObject({
      context: { state: 'processing', currentBook: { bookId: 'book-02' } },
    });

    await expect(api.getJob(cardsJobId)).resolves.toMatchObject({
      status: 'succeeded',
      result: { type: 'chapter_cards', sourceView: 'published', chapters: 2, built: 1 },
    });
    const ready = await api.getIndexingStatus(projectId, scope);
    expect(ready.published).toEqual({
      exists: true,
      context: { state: 'ready', currentBook: null, retryableFailedBook: null },
    });
    expect(ready.steps.slice(2)).toEqual([
      expect.objectContaining({
        kind: 'cards', status: 'succeeded', progress: 1, jobId: cardsJobId, counters: { indexedChapters: 1, totalChapters: 1 },
      }),
      { kind: 'ready', status: 'succeeded', progress: 1, bookId: 'book-02', sourceView: 'published' },
    ]);
    expect((await api.listProjectJobs(projectId, { kind: 'chapter_cards' })).data).toHaveLength(1);
  });

  it('offers a partial draft for a restart that makes every draft book ready', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: 'editor' });

    expect((await api.getIndexingStatus(projectId)).draft).toEqual({
      exists: true,
      context: { state: 'partial', currentBook: null, retryableFailedBook: { bookId: 'book-04' } },
    });
    await expect(api.getIndexingStatus(projectId, { bookId: 'book-04', sourceView: 'draft' })).resolves.toMatchObject({
      steps: [
        { kind: 'import', status: 'pending' },
        { kind: 'text_index', status: 'pending', counters: { indexedChapters: 0, totalChapters: 1 } },
        { kind: 'cards', status: 'pending', progress: 0 },
        { kind: 'ready', status: 'pending' },
      ],
    });

    const restart = await api.startProjectIndexing(projectId, { sourceView: 'draft' }, { idempotencyKey: 'idem-direct-draft-restart' });
    expect((await api.getIndexingStatus(projectId)).draft).toEqual({
      exists: true,
      context: { state: 'processing', currentBook: { bookId: 'book-04', position: 4, total: 4 }, retryableFailedBook: null },
    });
    await finishJob(api, restart.jobId);
    await expect(api.getBook('book-04')).resolves.toMatchObject({ status: 'ready' });
    const cardsJobId = (await api.listProjectJobs(projectId, { kind: 'chapter_cards' })).data[0]!.id;
    await expect(finishJob(api, cardsJobId)).resolves.toMatchObject({
      result: { type: 'chapter_cards', sourceView: 'draft', chapters: 5, built: 1 },
    });
    expect((await api.getIndexingStatus(projectId)).draft).toEqual({
      exists: true,
      context: { state: 'ready', currentBook: null, retryableFailedBook: null },
    });
    await expect(api.searchProject(projectId, 'порт', 'draft')).resolves.toMatchObject({
      textCoverage: { currentBooks: 4, totalBooks: 4, nonCurrentBooks: 0 },
    });
  });

  it('requires a complete scope for the exact book checklist', async () => {
    const api = createMockApiClient();

    await expect(api.getIndexingStatus(projectId, { bookId: 'book-02' })).rejects.toMatchObject({ status: 400 });
    await expect(api.getIndexingStatus(projectId, { bookId: 'book-03', sourceView: 'published' }))
      .rejects.toMatchObject({ operationId: 'getIndexingStatus', status: 404 });
    await expect(api.getIndexingStatus(projectId, { bookId: 'book-foreign', sourceView: 'published' }))
      .rejects.toMatchObject({ operationId: 'getIndexingStatus', status: 404 });
  });

  it('keeps search and status bound to the readable source view', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: 'viewer' });

    const publishedSearch = await api.searchProject(projectId, 'порт', 'published');
    expect(publishedSearch.textCoverage).toMatchObject({ nonCurrentBooks: 0, truncated: false });
    expect(publishedSearch.data.find((result) => result.kind === 'chapter')).toMatchObject({
      locator: { sourceRef: 'chapter-12', sourceView: 'published' },
    });
    await expect(api.searchProject(projectId, 'порт', undefined as never)).rejects.toMatchObject({ status: 400 });
    await expect(api.searchProject(projectId, 'порт', 'draft')).rejects.toMatchObject({ status: 404 });

    const viewerStatus = await api.getIndexingStatus(projectId);
    expect(Object.keys(viewerStatus).sort()).toEqual(['published', 'steps']);
    expect(viewerStatus.published.context).toHaveProperty('retryableFailedBook', null);

    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    const draftSearch = await api.searchProject(projectId, 'порт', 'draft');
    expect(draftSearch.textCoverage.nonCurrentBooks).toBeGreaterThan(0);
    expect(draftSearch.data.find((result) => result.kind === 'chapter')).toMatchObject({ locator: { sourceView: 'draft' } });
    const editorStatus = await api.getIndexingStatus(projectId);
    expect(Object.keys(editorStatus).sort()).toEqual(['draft', 'published', 'steps']);
    expect(editorStatus.draft?.context).toHaveProperty('retryableFailedBook', { bookId: 'book-04' });
  });
});
