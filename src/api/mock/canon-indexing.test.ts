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

  // specs/2026-11-12-desk-window.md WN3.4, WN3.5: the scenario of a build without its helper — the text index queues no
  // cards Job, and the reading of the saga waits for the helper with what is read, no time and no letter.
  it('waits for the helper and queues no cards without it', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: 'owner', helperMissing: true });
    await finishJob(api, 'canon-text-index-job-1');
    expect((await api.listProjectJobs(projectId, { kind: 'chapter_cards' })).data).toEqual([]);
    const status = await api.getIndexingStatus(projectId);
    expect(status.preparation).toMatchObject({ waiting: 'helper', remainingSeconds: null, letterPromised: false, currentBookId: 'book-02' });
    expect(status.preparation!.chaptersRead).toBeLessThan(status.preparation!.chaptersTotal);

    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    expect((await api.getIndexingStatus(projectId)).preparation?.waiting).toBeUndefined();
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
    // The published text of the mock saga is read: the reader sees the reading too (specs/2026-11-10-ux3-saga-state.md SS1.1).
    expect(Object.keys(viewerStatus).sort()).toEqual(['preparation', 'published', 'steps']);
    expect(viewerStatus.published.context).toHaveProperty('retryableFailedBook', null);

    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    const draftSearch = await api.searchProject(projectId, 'порт', 'draft');
    expect(draftSearch.textCoverage.nonCurrentBooks).toBeGreaterThan(0);
    expect(draftSearch.data.find((result) => result.kind === 'chapter')).toMatchObject({ locator: { sourceView: 'draft' } });
    const editorStatus = await api.getIndexingStatus(projectId);
    expect(Object.keys(editorStatus).sort()).toEqual(['draft', 'preparation', 'published', 'steps']);
    expect(editorStatus.draft?.context).toHaveProperty('retryableFailedBook', { bookId: 'book-04' });
  });

  // FS1, FS4.1, FS5 of the spec «Сага готова» (specs/2026-11-09): a new book opens the wave of the saga; while its
  // published text is prepared the status carries the preparation — the estimate and the promise of the letter once the
  // cards are written — and the cards that leave that text ready close the wave, which the status then tells of.
  it('opens the wave of the saga with a new book and tells when the saga is ready', async () => {
    const api = createMockApiClient();
    // The mock saga is read again without a wave: the reading is counted, with no estimate and no letter.
    const before = await api.getIndexingStatus(projectId);
    expect(before.preparation).toMatchObject({ remainingSeconds: null, letterPromised: false, chaptersRead: 1, chaptersTotal: 2 });
    expect(before.sagaReady).toBeUndefined();

    const started = await api.importBookFile(projectId, {
      file: new File(['word-document'], 'Том 5.docx', { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }),
      metadata: { expectedBookOrder: 5 },
    }, { idempotencyKey: 'idem-saga-wave' });
    const imported = await api.getJob(started.jobId);
    const bookId = imported.result?.type === 'import' ? imported.result.bookId : '';
    const scope = { bookId, sourceView: 'published' } as const;

    const indexing = await api.getIndexingStatus(projectId, scope);
    expect(indexing.published.context?.state).toBe('processing');
    // specs/2026-11-10-ux3-saga-state.md SS1.1: the status counts the reading of the saga — the first book of the mock
    // read, the second current (its chapter has no card yet), the new one waits and is not read.
    expect(indexing.preparation).toMatchObject({
      remainingSeconds: null, letterPromised: false, chaptersTotal: 3, booksTotal: 3, booksRead: 1, currentBookId: 'book-02', askable: true,
    });
    expect(indexing.preparation?.books.at(-1)).toEqual({ bookId, chaptersRead: 0, chaptersTotal: 1 });
    const textJobId = indexing.steps.find((step) => step.kind === 'text_index')?.jobId;
    if (!textJobId) throw new Error('Expected the text index of the published text.');
    await finishJob(api, textJobId);

    const cardsJobId = (await api.getIndexingStatus(projectId, scope)).steps.find((step) => step.kind === 'cards')?.jobId;
    if (!cardsJobId) throw new Error('Expected the cards of the published text.');
    await api.getJob(cardsJobId);
    const carding = await api.getIndexingStatus(projectId);
    expect(carding.preparation?.remainingSeconds).toBeGreaterThan(0);
    expect(carding.preparation?.letterPromised).toBe(true);
    expect(carding.sagaReady).toBeUndefined();

    // The author turned the letter off: the page promises none.
    await api.setAccountLetters({ sagaReady: false });
    expect((await api.getIndexingStatus(projectId)).preparation?.letterPromised).toBe(false);
    await api.setAccountLetters({ sagaReady: true });
    // A reader of the saga is not whom the wave is told to.
    api.setScenario({ preset: 'normal', actorRole: 'viewer' });
    expect((await api.getIndexingStatus(projectId)).preparation).toMatchObject({ letterPromised: false });
    api.setScenario({ preset: 'normal', actorRole: 'owner' });

    await api.getJob(cardsJobId);
    const ready = await api.getIndexingStatus(projectId);
    expect(ready.published.context?.state).toBe('ready');
    expect(ready.preparation).toBeUndefined();
    // The published text of the mock saga: two books of its own and the new one, a chapter each.
    expect(ready.sagaReady).toMatchObject({ books: 3, chapters: 3 });
    expect(Number.isNaN(Date.parse(ready.sagaReady!.at))).toBe(false);
    // Read again, the same readiness has the same time: the page tells of it once.
    expect((await api.getIndexingStatus(projectId)).sagaReady).toEqual(ready.sagaReady);
  });

  // specs/2026-11-10-first-questions.md FQ1.9: a saga with chapters and no map yet is pending — while its cards are written
  // and while the map of the first cards is built after them; the next read has it. A saga with its map, and one without a
  // chapter, are never pending.
  it('says the map is pending from the first chapter until it is built after the cards', async () => {
    const api = createMockApiClient();
    const ready = await api.getSagaFirstQuestions(projectId, { sourceView: 'published' });
    expect(ready).toMatchObject({ pending: false });
    expect(ready.questions).toHaveLength(3);

    const saga = await api.createProject({ title: 'Сага до карты' });
    expect(await api.getSagaFirstQuestions(saga.id, { sourceView: 'published' })).toEqual({ questions: [], pending: false });
    const started = await api.importBookFile(saga.id, {
      file: new File(['word-document'], 'Том 1.docx', { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }),
      metadata: { expectedBookOrder: 1 },
    }, { idempotencyKey: 'idem-map-pending' });
    const imported = await api.getJob(started.jobId);
    const scope = { bookId: imported.result?.type === 'import' ? imported.result.bookId : '', sourceView: 'published' } as const;
    const textJobId = (await api.getIndexingStatus(saga.id, scope)).steps.find((step) => step.kind === 'text_index')?.jobId;
    if (!textJobId) throw new Error('Expected the text index of the published text.');
    await finishJob(api, textJobId);
    // The chat opened right after the upload: the chapters are there, their cards are not yet.
    expect(await api.getSagaFirstQuestions(saga.id, { sourceView: 'published' })).toEqual({ questions: [], pending: true });
    const cardsJobId = (await api.getIndexingStatus(saga.id, scope)).steps.find((step) => step.kind === 'cards')?.jobId;
    if (!cardsJobId) throw new Error('Expected the cards of the published text.');
    await finishJob(api, cardsJobId);

    expect(await api.getSagaFirstQuestions(saga.id, { sourceView: 'published' })).toEqual({ questions: [], pending: true });
    expect(await api.getSagaFirstQuestions(saga.id, { sourceView: 'published' })).toEqual({ questions: [], pending: false });
  });
});
