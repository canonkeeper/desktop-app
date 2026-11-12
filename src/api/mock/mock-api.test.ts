import { describe, expect, it } from 'vitest';
import { createMockApiClient, MockApiError } from './index';
import { mockAccountToken } from './repository/session';

function importFileFixture(content: string): File {
  const bytes = new TextEncoder().encode(content);
  return {
    arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    lastModified: 1,
    name: 'same.epub',
    size: bytes.byteLength,
    type: 'application/epub+zip',
  } as File;
}

describe('mock API repository', () => {
  it('exposes canonical cover and Chat rename operations', async () => {
    const api = createMockApiClient();

    expect(api.uploadBookCover).toBeTypeOf('function');
    expect(api.deleteBookCover).toBeTypeOf('function');
    expect(api.renameChatSession).toBeTypeOf('function');
    await expect(api.getBook('book-02')).resolves.toHaveProperty('coverUrl');
  });

  it('versions cover metadata, removes it explicitly and preserves it through book restore', async () => {
	const api = createMockApiClient();
	const first = await api.uploadBookCover('book-02', new File(['cover'], 'cover.jpg', { type: 'image/jpeg' }));
	expect(first.coverUrl).toBe('/api/v1/books/book-02/cover/1');
	const second = await api.uploadBookCover('book-02', new File(['cover-2'], 'cover.png', { type: 'image/png' }));
	expect(second.coverUrl).toBe('/api/v1/books/book-02/cover/2');
	await api.deleteBook('book-02');
	await expect(api.restoreBook('book-02')).resolves.toMatchObject({ coverUrl: '/api/v1/books/book-02/cover/2' });
	await expect(api.deleteBookCover('book-02')).resolves.toMatchObject({ coverUrl: null });
  });

  it('renames Chat only after a valid authorized success', async () => {
	const api = createMockApiClient();
	const chat = await api.createChatSession(
	  'project-white-port',
	  { scope: 'project', sourceView: 'published', title: 'Исходное' },
	  { idempotencyKey: 'idem-rename-chat-create' },
	);
	await expect(api.renameChatSession(chat.id, { title: '  Новое  ' })).resolves.toMatchObject({ title: 'Новое' });
	api.setScenario({ preset: 'normal', actorRole: 'viewer' });
	await expect(api.renameChatSession(chat.id, { title: 'Viewer' })).rejects.toMatchObject({ status: 403 });
	api.setScenario({ preset: 'normal', actorRole: 'non_member' });
	await expect(api.renameChatSession(chat.id, { title: 'Outsider' })).rejects.toMatchObject({ status: 404 });
	api.setScenario({ preset: 'normal', actorRole: 'owner' });
	await expect(api.renameChatSession(chat.id, { title: ' ' })).rejects.toMatchObject({ status: 422, code: 'title_empty' });
	await expect(api.renameChatSession(chat.id, { title: 'я'.repeat(121) })).rejects.toMatchObject({ status: 422, code: 'title_too_long' });
	api.setScenario({
	  preset: 'normal',
	  operationFailures: { updateChatSession: { status: 409, message: 'Deletion race.', code: 'conflict' } },
	});
	await expect(api.renameChatSession(chat.id, { title: 'Не применять' })).rejects.toMatchObject({ status: 409 });
	api.setScenario({ preset: 'normal' });
	await expect(api.getChatSession(chat.id)).resolves.toMatchObject({ title: 'Новое' });
  });

  it('returns the ordered book checklist only for an exact scope and without side effects', async () => {
    const api = createMockApiClient();
    const scope = { bookId: 'book-02', sourceView: 'draft' } as const;
    const first = await api.getIndexingStatus('project-white-port', scope);
    const second = await api.getIndexingStatus('project-white-port', scope);

    expect(second).toEqual(first);
    expect(first.steps.map((step) => step.kind)).toEqual(['import', 'text_index', 'cards', 'ready']);
    expect(first.steps.every((step) => step.progress === null || typeof step.progress === 'number')).toBe(true);
    await expect(api.getIndexingStatus('project-white-port')).resolves.toMatchObject({ steps: [] });
  });

  // specs/2026-11-09-demo-saga.md DS1.2: «Попробовать на примере» makes the demo saga of the author once, with its book.
  it('opens one demo saga with its book ready, and the same one again', async () => {
    const api = createMockApiClient();
    const before = (await api.listProjects()).data.length;
    const saga = await api.openDemoSaga();
    expect(saga).toMatchObject({ demo: true, title: 'Пример: «Игрок» Достоевского', bookCount: 1, currentMembership: { role: 'owner' } });
    expect(saga.description).toContain('Текст: Викитека, CC BY-SA 4.0');
    expect((await api.openDemoSaga()).id).toBe(saga.id);
    const sagas = (await api.listProjects()).data;
    expect(sagas).toHaveLength(before + 1);
    expect(sagas.filter((project) => project.demo)).toHaveLength(1);
    expect((await api.listBooks(saga.id)).data.map((book) => book.title)).toEqual(['Игрок']);
    await expect(api.getIndexingStatus(saga.id)).resolves.toMatchObject({ published: { exists: true, context: { state: 'ready' } } });
    // A saga of the author carries no sign.
    expect((await api.createProject({ title: 'Своя сага' })).demo).toBeUndefined();
  });

  it('keeps state in memory and resets to seeded data', async () => {
    const api = createMockApiClient();
    const initialCount = (await api.listProjects()).data.length;
    await api.createProject({ title: 'Новый проект' });

    expect((await api.listProjects()).data).toHaveLength(initialCount + 1);

    api.reset();
    expect((await api.listProjects()).data).toHaveLength(initialCount);
  });

  // specs/2026-11-08-italic-bold-in-text.md TF3.2: the book keeps the italic and bold of a working text in markdown, its
  // words in text; «Править главу» and «Вернуть» give the working text its marks back.
  it('carries the faces between the book and the working text as the server does', async () => {
    const api = createMockApiClient();
    const opened = await api.openChapterEdit('project-white-port', 'chapter-16');
    const before = opened.text;
    const text = 'Сава ⟨молчал⟩, и это было ⟪громче⟫ * * *\n⟦Знак⟧ на полях.';
    const written = await api.updateChapterDraft('project-white-port', opened.id, { expectedRevision: opened.revision, text });
    const replaced = await api.replaceChapterFromDraft('project-white-port', opened.id, { expectedRevision: written.revision, title: 'Белый порт' });
    expect(replaced.text).toBe('Сава ⟨молчал⟩, и это было ⟪громче⟫ * * *\n\nЗнак на полях.');
    const chapter = await api.getChapter('chapter-16', 'published');
    expect(chapter.paragraphs.map((paragraph) => [paragraph.markdown, paragraph.text])).toEqual([
      ['Сава *молчал*, и это было **громче** \\* \\* \\*', 'Сава молчал, и это было громче * * *'], ['Знак на полях.', 'Знак на полях.'],
    ]);
    const reverted = await api.revertChapterFromDraft('project-white-port', opened.id, { expectedRevision: replaced.revision });
    expect(reverted.text).toBe(before);
  });

  it('selects the requested chapter variant and conceals draft from viewers', async () => {
    const api = createMockApiClient();

    await expect(api.getChapter('chapter-16', 'published')).resolves.toMatchObject({ contentVariant: 'published', revision: 7 });
    await expect(api.getChapter('chapter-16', 'draft')).resolves.toMatchObject({ contentVariant: 'draft', revision: 9 });

    api.setScenario({ preset: 'normal', actorRole: 'viewer' });
    await expect(api.getChapter('chapter-16', 'draft')).rejects.toMatchObject({ operationId: 'getChapter', status: 404 });
    await expect(api.getChapter('chapter-16', 'published')).resolves.toMatchObject({ contentVariant: 'published', revision: 7 });
  });

  it('makes deleted book content immediately invisible from derived mock surfaces', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(
      'project-white-port',
      { scope: 'project', sourceView: 'published', title: 'Порт' },
      { idempotencyKey: 'idem-direct-deleted-book-chat' },
    );
    const turn = await api.createChatTurn(chat.id, { body: 'Кто первым заметил дым?' }, { idempotencyKey: 'idem-direct-deleted-book-turn' });

    await api.deleteBook('book-02');

    await expect(api.getBook('book-02')).rejects.toMatchObject({ operationId: 'getBook', status: 404 });
    await expect(api.getChapter('chapter-12', 'published')).rejects.toMatchObject({ operationId: 'getChapter', status: 404 });
    await expect(api.getJob(turn.jobId)).rejects.toMatchObject({ operationId: 'getJob', status: 404 });

    const search = await api.searchProject('project-white-port', 'порт', 'published');
    expect(search.data).toEqual([]);
  });

  it('rejects protected direct repository operations after logout', async () => {
    const api = createMockApiClient();

    await expect(api.listProjects()).resolves.toMatchObject({ data: expect.any(Array) });
    await api.logout();
    await expect(api.logout()).resolves.toBeUndefined();

    await expect(api.getCurrentUser()).rejects.toMatchObject({ operationId: 'getCurrentUser', status: 401 });
    await expect(api.listProjects()).rejects.toMatchObject({ operationId: 'listProjects', status: 401 });
    await expect(api.createBook('project-white-port', { title: 'После выхода' })).rejects.toMatchObject({ operationId: 'createBook', status: 401 });

    api.setScenario({ preset: 'forbidden-403' });
    await expect(api.listProjects()).rejects.toMatchObject({ operationId: 'listProjects', status: 401 });
    api.setScenario({ preset: 'request-error' });
    await expect(api.createBook('project-white-port', { title: 'После выхода' })).rejects.toMatchObject({ operationId: 'createBook', status: 401 });

    api.setScenario({ preset: 'normal' });
    await api.login({ email: 'mira@example.com', password: 'white-port-12', rememberMe: false });
    await expect(api.listProjects()).resolves.toMatchObject({ data: expect.any(Array) });
  });

  it('projects public job state and authorizes cancel for creator, admin and owner', async () => {
    const creatorApi = createMockApiClient();
    creatorApi.setScenario({ preset: 'normal', actorRole: 'editor' });
    const started = await creatorApi.startProjectIndexing(
      'project-white-port',
      { sourceView: 'published' },
      { idempotencyKey: 'idem-direct-creator-viewer-cancel' },
    );
    expect(started.job).toMatchObject({
      status: 'queued',
      result: null,
      canCancel: true,
      links: { cancel: `/api/v1/jobs/${started.jobId}/cancel` },
    });

    creatorApi.setScenario({ preset: 'normal', actorRole: 'viewer' });
    await expect(creatorApi.getJob(started.jobId)).resolves.toMatchObject({
      canCancel: true,
      links: { cancel: `/api/v1/jobs/${started.jobId}/cancel` },
    });
    await expect(creatorApi.cancelJob(started.jobId)).resolves.toMatchObject({
      status: 'canceled',
      result: null,
      error: null,
      canCancel: false,
      links: { cancel: null },
    });

    const viewerApi = createMockApiClient();
    viewerApi.setScenario({ preset: 'normal', actorRole: 'viewer' });
    await expect(viewerApi.getJob('canon-text-index-job-1')).resolves.toMatchObject({ canCancel: false, links: { cancel: null } });
    await expect(viewerApi.cancelJob('canon-text-index-job-1')).rejects.toMatchObject({ operationId: 'cancelJob', status: 403 });

    for (const actorRole of ['admin', 'owner'] as const) {
      const managerApi = createMockApiClient();
      managerApi.setScenario({ preset: 'normal', actorRole });
      await expect(managerApi.cancelJob('canon-text-index-job-1')).resolves.toMatchObject({
        status: 'canceled',
        canCancel: false,
        links: { cancel: null },
      });
    }
  });

  it('returns only the public projection from async start responses', async () => {
    const api = createMockApiClient();
    const started = await api.importBookFile(
      'project-white-port',
      { file: importFileFixture('aaaa') },
      { idempotencyKey: 'idem-direct-public-start' },
    );

    expect(started.job).toMatchObject({
      kind: 'import',
      status: 'queued',
      result: null,
      canCancel: true,
      links: { cancel: `/api/v1/jobs/${started.jobId}/cancel` },
    });

    const canceled = await api.cancelJob(started.jobId);
    expect(canceled).toMatchObject({
      status: 'canceled',
      result: null,
      canCancel: false,
      links: { cancel: null },
    });
  });

  it('paginates contract list endpoints and rejects foreign cursors', async () => {
    const api = createMockApiClient();

    const firstBooksPage = await api.listBooks('project-white-port', { limit: 1 });
    expect(firstBooksPage.data).toHaveLength(1);
    expect(firstBooksPage.meta.nextCursor).toBeTruthy();
    const secondBooksPage = await api.listBooks('project-white-port', {
      cursor: firstBooksPage.meta.nextCursor,
      limit: 1,
    });
    expect(secondBooksPage.data[0]?.id).not.toBe(firstBooksPage.data[0]?.id);

    await expect(api.listChapters('book-02', { cursor: 'bad-cursor', limit: 1 })).rejects.toMatchObject({
      operationId: 'listChapters',
      status: 400,
    });
  });

  it('lists bounded deleted sources and restores project, book and chapter state', async () => {
    const projectApi = createMockApiClient();
    await projectApi.deleteProject('project-white-port');
    await expect(projectApi.getProject('project-white-port')).rejects.toMatchObject({ operationId: 'getProject', status: 404 });
    const deletedProjects = await projectApi.listProjects({ state: 'deleted' });
    expect(deletedProjects.data).toHaveLength(1);
    expect(Object.keys(deletedProjects.data[0]!).sort()).toEqual(['deletedAt', 'id', 'restoreUntil', 'title']);
    expect(Date.parse(deletedProjects.data[0]!.restoreUntil) - Date.parse(deletedProjects.data[0]!.deletedAt)).toBe(7 * 24 * 60 * 60 * 1000);
    await expect(projectApi.restoreProject('project-white-port')).resolves.toMatchObject({ id: 'project-white-port' });
    await expect(projectApi.getProject('project-white-port')).resolves.toMatchObject({ id: 'project-white-port' });
    await expect(projectApi.listProjects({ state: 'deleted' })).resolves.toMatchObject({ data: [] });

    const bookApi = createMockApiClient();
    await bookApi.deleteBook('book-02');
    await expect(bookApi.getBook('book-02')).rejects.toMatchObject({ operationId: 'getBook', status: 404 });
    const deletedBooks = await bookApi.listBooks('project-white-port', { state: 'deleted' });
    expect(deletedBooks.data).toHaveLength(1);
    expect(Object.keys(deletedBooks.data[0]!).sort()).toEqual(['deletedAt', 'id', 'restoreUntil', 'title']);
    expect(Date.parse(deletedBooks.data[0]!.restoreUntil) - Date.parse(deletedBooks.data[0]!.deletedAt)).toBe(7 * 24 * 60 * 60 * 1000);
    await expect(bookApi.restoreBook('book-02')).resolves.toMatchObject({ id: 'book-02' });
    await expect(bookApi.getBook('book-02')).resolves.toMatchObject({ id: 'book-02' });
    await expect(bookApi.getChapter('chapter-12', 'published')).resolves.toMatchObject({ id: 'chapter-12' });
    await expect(bookApi.listBooks('project-white-port', { state: 'deleted' })).resolves.toMatchObject({ data: [] });

    const chapterApi = createMockApiClient();
    await chapterApi.deleteChapter('chapter-16');
    await expect(chapterApi.getChapter('chapter-16', 'draft')).rejects.toMatchObject({ operationId: 'getChapter', status: 404 });
    const deletedChapters = await chapterApi.listChapters('book-02', { state: 'deleted' });
    expect(deletedChapters.data).toHaveLength(1);
    expect(Object.keys(deletedChapters.data[0]!).sort()).toEqual(['deletedAt', 'id', 'restoreUntil', 'title']);
    expect(Date.parse(deletedChapters.data[0]!.restoreUntil) - Date.parse(deletedChapters.data[0]!.deletedAt)).toBe(7 * 24 * 60 * 60 * 1000);
    await expect(chapterApi.restoreChapter('chapter-16')).resolves.toMatchObject({ id: 'chapter-16' });
    await expect(chapterApi.getChapter('chapter-16', 'draft')).resolves.toMatchObject({ id: 'chapter-16' });
    await expect(chapterApi.listChapters('book-02', { state: 'deleted' })).resolves.toMatchObject({ data: [] });
  });

  it('validates import file controls and keeps book reorder commands out of book resources', async () => {
    const api = createMockApiClient();

    await expect(api.getImportConstraints('project-white-port')).resolves.not.toHaveProperty('nextBookOrder');

    await expect(
      api.importBookFile(
        'project-white-port',
        { file: new File(['binary'], 'draft.exe', { type: 'application/octet-stream' }) },
        { idempotencyKey: 'idem-direct-bad-import-type' },
      ),
    ).rejects.toMatchObject({
      message: 'Import file type is not supported.',
      operationId: 'importBookFile',
      status: 415,
    });

    const moved = await api.updateBook('book-02', { afterBookId: null });
    expect(moved).not.toHaveProperty('afterBookId');
    expect(moved).toMatchObject({
      displayNumber: 'I',
      order: 1,
    });

    const books = await api.listBooks('project-white-port');
    expect(books.data.map((book) => book.id).slice(0, 2)).toEqual(['book-02', 'book-01']);
    expect(books.data.every((book) => !Object.hasOwn(book, 'afterBookId'))).toBe(true);
  });

  it('rejects restoring a deleted book when another active book occupies its order', async () => {
    const api = createMockApiClient();
    await api.deleteBook('book-02');
    await api.updateBook('book-03', { afterBookId: 'book-01' });

    await expect(api.restoreBook('book-02')).rejects.toMatchObject({
      operationId: 'restoreBook',
      status: 409,
    });
    await expect(api.getBook('book-02')).rejects.toMatchObject({ status: 404 });
    await expect(api.listBooks('project-white-port', { state: 'deleted' })).resolves.toMatchObject({
      data: [expect.objectContaining({ id: 'book-02' })],
    });
  });

  // specs/2026-11-11-ux7-upload.md UP2.3: a new book stands after the books of the shelf, not in the place of a deleted one.
  it('puts a new book after the books of the shelf', async () => {
	const api = createMockApiClient();
	await api.deleteBook('book-02');

	const inserted = await api.createBook('project-white-port', { title: 'Новый том' });
	expect(inserted).toMatchObject({ place: 4 });
	const books = await api.listBooks('project-white-port');
	expect(books.data.map((book) => book.id)).toEqual(['book-01', 'book-03', 'book-04', inserted.id]);
  });

  it('rejects a stale expected import order', async () => {
    const api = createMockApiClient();

    await expect(api.importBookFile(
      'project-white-port',
      {
        file: new File(['book'], 'Том 4.epub', { type: 'application/epub+zip' }),
        metadata: { expectedBookOrder: 4 },
      },
      { idempotencyKey: 'idem-stale-expected-order' },
    )).rejects.toMatchObject({
      code: 'import_expected_order_conflict',
      operationId: 'importBookFile',
      status: 409,
    });


  });

  it('completes an import job with its book and source file', async () => {
    const api = createMockApiClient();
    const sourceFileName = 'Том 5.docx';
    const started = await api.importBookFile(
      'project-white-port',
      {
        file: new File(['word-document'], sourceFileName, {
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        }),
        metadata: { expectedBookOrder: 5 },
      },
      { idempotencyKey: 'idem-direct-complete-import' },
    );

    expect(started.job).toMatchObject({ kind: 'import', status: 'queued', result: null });
    await expect(api.getJob(started.jobId)).resolves.toMatchObject({
      kind: 'import',
      status: 'succeeded',
      progress: 1,
      canCancel: false,
      result: {
        type: 'import',
        bookId: 'import-book-3',
        importMode: 'new_book',
        sourceFileName,
      },
    });
  });

  it('makes an imported book ready after the draft text index and its checklist after the cards', async () => {
    const api = createMockApiClient();
    const started = await api.importBookFile(
      'project-white-port',
      {
        file: new File(['word-document'], 'Том 5.docx', {
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        }),
        metadata: { expectedBookOrder: 5 },
      },
      { idempotencyKey: 'idem-scoped-import-ready' },
    );

    const terminal = await api.getJob(started.jobId);
    const bookId = terminal.result?.type === 'import' ? terminal.result.bookId : undefined;
    if (!bookId) throw new Error('Expected the imported book id.');
    const scope = { bookId, sourceView: 'draft' } as const;
    const imported = await api.getIndexingStatus('project-white-port', scope);
    expect(imported.steps).toMatchObject([
      { kind: 'import', status: 'succeeded', importPhase: 'saved', bookId, jobId: started.jobId },
      { kind: 'text_index', status: 'queued', sourceView: 'draft', counters: { indexedChapters: 0, totalChapters: 1 } },
      { kind: 'cards', status: 'pending' },
      { kind: 'ready', status: 'pending' },
    ]);
    await expect(api.getBook(bookId)).resolves.toMatchObject({ status: 'draft' });

    const textJobId = imported.steps[1]!.jobId!;
    await api.getJob(textJobId);
    await expect(api.getJob(textJobId)).resolves.toMatchObject({ status: 'succeeded' });
    await expect(api.getBook(bookId)).resolves.toMatchObject({ status: 'ready' });
    const indexed = await api.getIndexingStatus('project-white-port', scope);
    expect(indexed.steps).toMatchObject([
      { kind: 'import', status: 'succeeded' },
      { kind: 'text_index', status: 'succeeded', progress: 1 },
      { kind: 'cards', status: 'queued', progress: 0 },
      { kind: 'ready', status: 'pending' },
    ]);

    const cardsJobId = indexed.steps[2]!.jobId!;
    await api.getJob(cardsJobId);
    await expect(api.getJob(cardsJobId)).resolves.toMatchObject({ kind: 'chapter_cards', status: 'succeeded' });
    await expect(api.getIndexingStatus('project-white-port', scope)).resolves.toMatchObject({
      steps: [
        { kind: 'import', status: 'succeeded' },
        { kind: 'text_index', status: 'succeeded' },
        { kind: 'cards', status: 'succeeded', progress: 1 },
        { kind: 'ready', status: 'succeeded', progress: 1, bookId, sourceView: 'draft' },
      ],
    });
  });

  it('fails import finalization when a concurrent append occupies its expected order', async () => {
    const api = createMockApiClient();
    const started = await api.importBookFile(
      'project-white-port',
      {
        file: new File(['volume-5'], 'Том 5.epub', { type: 'application/epub+zip' }),
        metadata: { expectedBookOrder: 5 },
      },
      { idempotencyKey: 'idem-concurrent-import-volume-5' },
    );

    await api.createBook('project-white-port', { title: 'Конкурентный том 5' });
    await expect(api.getJob(started.jobId)).resolves.toMatchObject({
      status: 'failed',
      error: { code: 'import_expected_order_conflict' },
    });
    const books = await api.listBooks('project-white-port');
    expect(books.data.filter((book) => book.order === 5)).toHaveLength(1);
  });

  it('advances the authoritative order across sequential imports', async () => {
    const api = createMockApiClient();
    const importVolume = async (order: number) => {
      const started = await api.importBookFile(
        'project-white-port',
        {
          file: new File([`volume-${order}`], `Том ${order}.epub`, { type: 'application/epub+zip' }),
          metadata: { expectedBookOrder: order },
        },
        { idempotencyKey: `idem-direct-import-volume-${order}` },
      );
      return api.getJob(started.jobId);
    };

    const first = await importVolume(5);
    const second = await importVolume(6);
    if (first.result?.type !== 'import' || second.result?.type !== 'import') {
      throw new Error('Expected terminal import results.');
    }
    expect(first.result.bookId).not.toBe(second.result.bookId);
    await expect(api.listBooks('project-white-port')).resolves.toMatchObject({
      data: expect.arrayContaining([
        expect.objectContaining({ id: first.result.bookId, order: 5, status: 'draft' }),
        expect.objectContaining({ id: second.result.bookId, order: 6, status: 'draft' }),
      ]),
    });
  });

  it('rejects a DOCX File with an empty or mismatched MIME type', async () => {
    const api = createMockApiClient();

    for (const [type, idempotencyKey] of [
      ['', 'idem-direct-empty-docx-mime'],
      ['application/epub+zip', 'idem-direct-mismatched-docx-mime'],
    ] as const) {
      await expect(api.importBookFile(
        'project-white-port',
        { file: new File(['word-document'], 'draft.docx', { type }) },
        { idempotencyKey },
      )).rejects.toMatchObject({
        message: 'Import file type is not supported.',
        operationId: 'importBookFile',
        status: 415,
      });
    }
  });

  it('enforces direct repository idempotency replay, mismatch and creator cancel semantics', async () => {
    const api = createMockApiClient();
    const importInput = {
      file: importFileFixture('aaaa'),
      metadata: { title: 'Same import' },
      options: { importMode: 'new_book' as const },
    };
    const firstImport = await api.importBookFile('project-white-port', importInput, { idempotencyKey: 'idem-direct-import-file' });
    const replayedImport = await api.importBookFile('project-white-port', importInput, { idempotencyKey: 'idem-direct-import-file' });
    expect(replayedImport.jobId).toBe(firstImport.jobId);
    await expect(api.importBookFile('project-white-port', {
      ...importInput,
      file: importFileFixture('bbbb'),
    }, { idempotencyKey: 'idem-direct-import-file' })).rejects.toMatchObject({
      operationId: 'importBookFile',
      status: 409,
    });
    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    const indexRun = await api.startProjectIndexing(
      'project-white-port',
      { sourceView: 'published' },
      { idempotencyKey: 'idem-direct-index' },
    );
    expect(indexRun.job).toMatchObject({ canCancel: true, status: 'queued' });
    await expect(api.cancelJob(indexRun.jobId)).resolves.toMatchObject({ status: 'canceled' });
  });

  it('enforces owner governance for project deletion and membership changes', async () => {
    const api = createMockApiClient();

    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    await expect(api.deleteProject('project-white-port')).rejects.toMatchObject({ operationId: 'deleteProject', status: 403 });

    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    await expect(api.updateProjectMemberRole('project-white-port', 'member-owner', { role: 'viewer' })).rejects.toMatchObject({
      operationId: 'updateProjectMemberRole',
      status: 409,
    });
    await expect(api.removeProjectMember('project-white-port', 'member-owner')).rejects.toMatchObject({
      operationId: 'removeProjectMember',
      status: 409,
    });
    await expect(api.updateProject('project-white-port', { id: 'project-hijack', title: 'Нельзя менять id' } as never)).rejects.toMatchObject({
      operationId: 'updateProject',
      status: 400,
    });
    await expect(api.getProject('project-white-port')).resolves.toMatchObject({ id: 'project-white-port' });
    await expect(api.updateProjectMemberRole('project-white-port', 'member-collaborator', { role: 'owner' } as never)).rejects.toMatchObject({
      operationId: 'updateProjectMemberRole',
      status: 400,
    });
    await expect(api.updateProjectMemberRole('project-white-port', 'member-collaborator', { role: 'viewer' })).resolves.toMatchObject({
      id: 'member-collaborator',
      role: 'viewer',
    });
    await expect(api.removeProjectMember('project-white-port', 'member-collaborator')).resolves.toBeUndefined();
  });

  it('enforces invitation acceptance email, lifecycle and replay state in the direct repository', async () => {
    const api = createMockApiClient();

    api.setScenario({ preset: 'normal', actorRole: 'non_member', verifiedEmail: 'mira@example.com' });
    await expect(api.listMyProjectInvitations({ limit: 10 })).resolves.toMatchObject({
      data: [
        expect.objectContaining({
          id: 'invitation-mira',
          project: { id: 'project-white-port', title: 'Белый порт' },
          inviter: { displayName: 'Мира Волкова', email: 'mira@example.com' },
        }),
      ],
    });
    await expect(api.acceptProjectInvitation('invitation-mira')).resolves.toMatchObject({
      id: 'member-invitation-mira',
      email: 'mira@example.com',
      role: 'editor',
      status: 'active',
      userId: 'user-mira',
    });
    await expect(api.getProject('project-white-port')).resolves.toMatchObject({
      currentMembership: expect.objectContaining({ role: 'editor', userId: 'user-mira' }),
      id: 'project-white-port',
    });
    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    await expect(api.listProjectMembers('project-white-port')).resolves.toMatchObject({
      data: expect.arrayContaining([
        expect.objectContaining({ email: 'mira@example.com', id: 'member-invitation-mira', role: 'editor', status: 'active', userId: 'user-mira' }),
      ]),
    });
    api.setScenario({ preset: 'normal', actorRole: 'non_member', verifiedEmail: 'mira@example.com' });
    await expect(api.acceptProjectInvitation('invitation-mira')).rejects.toMatchObject({
      message: 'Invitation is not pending.',
      operationId: 'acceptProjectInvitation',
      status: 409,
    });

    api.reset();
    api.setScenario({ preset: 'normal', actorRole: 'editor', verifiedEmail: 'mira@example.com' });
    await expect(api.acceptProjectInvitation('invitation-mira')).rejects.toMatchObject({
      message: 'Нет доступа к этому проекту.',
      operationId: 'acceptProjectInvitation',
      status: 403,
    });

    api.setScenario({ preset: 'normal', actorRole: 'non_member', verifiedEmail: 'other@example.com' });
    await expect(api.acceptProjectInvitation('invitation-mira')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'acceptProjectInvitation',
      status: 404,
    });
    await expect(api.acceptProjectInvitation('invitation-accepted')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'acceptProjectInvitation',
      status: 404,
    });
    await expect(api.acceptProjectInvitation('invitation-canceled')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'acceptProjectInvitation',
      status: 404,
    });

    api.setScenario({ preset: 'normal', actorRole: 'non_member', emailVerified: false, verifiedEmail: 'mira@example.com' });
    await expect(api.acceptProjectInvitation('invitation-mira')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'acceptProjectInvitation',
      status: 404,
    });

    api.setScenario({ preset: 'normal', actorRole: 'non_member' });
    await expect(api.acceptProjectInvitation('invitation-1')).rejects.toMatchObject({ status: 404 });
    await expect(api.acceptProjectInvitation('invitation-accepted')).rejects.toMatchObject({ status: 404 });
    await expect(api.acceptProjectInvitation('invitation-canceled')).rejects.toMatchObject({ status: 404 });
    await expect(api.acceptProjectInvitation('invitation-missing-123')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'acceptProjectInvitation',
      status: 404,
    });
  });

  it('rejects registration when accepted terms are missing or false', async () => {
    const api = createMockApiClient();

    await expect(
      api.register({ acceptedTerms: false, displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' } as never),
    ).rejects.toMatchObject({ operationId: 'registerUser', status: 400 });
    await expect(api.register({ displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' } as never)).rejects.toMatchObject({
      operationId: 'registerUser',
      status: 400,
    });
  });

  // UC1.1: a new address signs in unverified; a taken one is 409; the mock link verifies it.
  it('registers a new address unverified, refuses a taken one and verifies by the mock link', async () => {
    const api = createMockApiClient();

    await expect(api.register({
      acceptedTerms: true,
      displayName: 'Мира Волкова',
      email: 'mira@example.com',
      password: 'white-port-12',
      acceptedPersonalDataConsent: true,
    })).rejects.toMatchObject({ operationId: 'registerUser', status: 409, problem: { code: 'auth.email_taken' } });
    const registered = await api.register({
      acceptedTerms: true,
      displayName: 'Новый автор',
      email: 'new-author@example.test',
      password: 'white-port-12',
      acceptedPersonalDataConsent: true,
    });

    expect(registered.user).toMatchObject({ email: 'new-author@example.test', emailVerified: false });
    await expect(api.sendEmailVerification()).resolves.toEqual({ status: 'accepted' });
    const foreignLink = mockAccountToken('email_verification', 'someone');
    await expect(api.confirmEmailVerification({ token: foreignLink })).rejects.toMatchObject({
      status: 400, problem: { code: 'auth.token_invalid' },
    });
    const ownLink = mockAccountToken('email_verification', registered.user.id);
    await expect(api.confirmEmailVerification({ token: ownLink })).resolves.toEqual({ status: 'verified' });
    expect((await api.getCurrentUser()).emailVerified).toBe(true);
    await expect(api.sendEmailVerification()).rejects.toMatchObject({ status: 409, problem: { code: 'auth.email_already_verified' } });
  });

  // UC2: one answer for any address; the mock reset link signs in and verifies the address.
  it('answers every reset request alike and resets by the mock link', async () => {
    const api = createMockApiClient();
    await expect(api.requestPasswordReset({ email: 'nobody@example.test' })).resolves.toEqual({ status: 'accepted' });
    await expect(api.requestPasswordReset({ email: 'mira@example.com' })).resolves.toEqual({ status: 'accepted' });
    const resetLink = mockAccountToken('password_reset', 'user-mira');
    const foreignLink = mockAccountToken('password_reset', 'someone');
    await expect(api.resetPassword({ token: foreignLink, newPassword: 'white-port-12' })).rejects.toMatchObject({
      status: 400, problem: { code: 'auth.token_invalid' },
    });
    const session = await api.resetPassword({ token: resetLink, newPassword: 'white-port-12' });
    expect(session.user).toMatchObject({ email: 'mira@example.com', emailVerified: true });
  });

  // UC3: a wrong current password is refused; «выйти везде» ends the mock session.
  it('changes the password and signs out everywhere', async () => {
    const api = createMockApiClient();
    await api.login({ email: 'mira@example.com', password: 'white-port-12', rememberMe: false });
    await expect(api.changePassword({ currentPassword: 'wrong-password', newPassword: 'white-port-13' })).rejects.toMatchObject({
      status: 400, problem: { code: 'auth.current_password_invalid' },
    });
    await expect(api.changePassword({ currentPassword: 'white-port-12', newPassword: 'white-port-13' })).resolves.toBeUndefined();
    await api.logoutEverywhere();
    await expect(api.getCurrentUser()).rejects.toMatchObject({ status: 401 });
  });

  // Legal R3.2: the acceptance needs both marks and records the current documents.
  it('accepts the current documents with both marks', async () => {
    const api = createMockApiClient();
    await api.login({ email: 'mira@example.com', password: 'white-port-12', rememberMe: false });
    await expect(api.acceptLegalDocuments({ acceptedTerms: true, acceptedPersonalDataConsent: false } as never)).rejects.toMatchObject({ status: 400 });
    await expect(api.acceptLegalDocuments({ acceptedTerms: true, acceptedPersonalDataConsent: true })).resolves.toBeUndefined();
    expect((await api.getCurrentUser()).documentsAccepted).toBe(true);
  });

  // UC1.4: model work answers 403 auth.email_unverified until the address is verified.
  it('refuses model work for an unverified address', async () => {
    const api = createMockApiClient();
    await api.register({ acceptedTerms: true, acceptedPersonalDataConsent: true, displayName: 'Новый автор', email: 'new-author@example.test', password: 'white-port-12' });

    await expect(api.startProjectIndexing('project-white-port', { sourceView: 'draft' }, { idempotencyKey: 'idem-unverified-index' }))
      .rejects.toMatchObject({ status: 403, problem: { code: 'auth.email_unverified' } });
  });

  it('keeps direct-id miss messages generic and rejects unsupported search filters', async () => {
    const api = createMockApiClient();

    await expect(api.getJob('job-missing-123')).rejects.toMatchObject({
      message: 'Resource not found.',
      operationId: 'getJob',
      status: 404,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { filters: { unsupported: true } as never })).rejects.toMatchObject({
      message: 'Search filters contain unsupported keys.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'x'.repeat(501), 'published', 'all')).rejects.toMatchObject({
      message: 'Search query must be 500 characters or fewer.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', '', 'published', 'all')).rejects.toMatchObject({
      message: 'Search query must be a non-empty string.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'bad-scope' as never)).rejects.toMatchObject({
      message: 'Search scope is invalid.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { limit: 51 })).rejects.toMatchObject({
      message: 'Search limit must be an integer between 1 and 50.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { filters: { bookId: 123 } as never })).rejects.toMatchObject({
      message: 'Search ID filters must be non-empty strings.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { filters: { chapterId: false } as never })).rejects.toMatchObject({
      message: 'Search ID filters must be non-empty strings.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { filters: { resultKinds: ['chapter', 'chapter'] } as never })).rejects.toMatchObject({
      message: 'Search resultKinds filter is invalid.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(
      api.searchProject('project-white-port', 'port', 'published', 'all', {
        filters: { resultKinds: ['chapter', 'annotation', 'unknown-kind'] } as never,
      }),
    ).rejects.toMatchObject({
      message: 'Search resultKinds filter is invalid.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { unsupported: true } as never)).rejects.toMatchObject({
      message: 'Search request contains unsupported keys.',
      operationId: 'searchProject',
      status: 400,
    });
    await expect(api.searchProject('project-white-port', 'port', 'published', 'all', { signal: new AbortController().signal })).resolves.toMatchObject({ query: 'port' });
  });

  it('injects deterministic fallback scenarios through the API boundary', async () => {
    const api = createMockApiClient();

    api.setScenario({ preset: 'auth-401' });
    await expect(api.getCurrentUser()).rejects.toMatchObject({ status: 401 });
    await expect(api.listMyProjectInvitations()).rejects.toMatchObject({ operationId: 'listMyProjectInvitations', status: 401 });
    await expect(api.createBook('project-white-port', { title: 'Закрытая книга' })).rejects.toMatchObject({ operationId: 'createBook', status: 401 });

    api.setScenario({ preset: 'forbidden-403' });
    await expect(api.deleteBook('book-02')).rejects.toMatchObject({ operationId: 'deleteBook', status: 403 });

    api.setScenario({ preset: 'empty-lists' });
    await expect(api.listProjects()).resolves.toMatchObject({ data: [] });

    api.setScenario({ preset: 'normal', actorRole: 'non_member' });
    await expect(api.listProjects()).resolves.toMatchObject({ data: [] });

    api.setScenario({ preset: 'normal', actorRole: 'editor' });
    await expect(api.cancelJob('canon-text-index-job-1')).rejects.toMatchObject({ operationId: 'cancelJob', status: 403 });

    api.setScenario({ preset: 'failed-create' });
    await expect(api.createProject({ title: 'x' })).rejects.toBeInstanceOf(MockApiError);

    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    await expect(api.cancelJob('canon-text-index-job-1')).resolves.toMatchObject({ status: 'canceled' });
  });
});
