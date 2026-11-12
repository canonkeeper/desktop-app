import { describe, expect, it } from 'vitest';
import { createMockApiClient, type MockRepository } from '../index';

const projectId = 'project-white-port';

function book(volume: number, type = 'application/x-fictionbook+xml') {
  return new File([`volume-${volume}`], `том ${volume}.fb2`, { type });
}

/** A queue of the volumes in this order; a volume of `unchecked` sends a file the check of the server does not pass. */
async function importQueue(volumes: number[], unchecked: number[] = [], api: MockRepository = createMockApiClient()) {
  const created = await api.createImportQueue(projectId, {
    items: volumes.map((volume, index) => ({
      clientItemId: `client-${volume}`,
      position: index + 1,
      sourceFileName: `том ${volume}.fb2`,
    })),
  }, { idempotencyKey: `idem-create-${volumes.join('-')}` });

  for (const [index, item] of created.items.entries()) {
    const volume = volumes[index]!;
    await api.uploadImportQueueItem(projectId, created.id, {
      itemId: item.id,
      file: book(volume, unchecked.includes(volume) ? 'text/plain' : undefined),
    }, { idempotencyKey: `idem-upload-${volumes.join('-')}-${item.id}` });
  }
  const uploaded = await api.getImportQueue(projectId, created.id);
  const started = await api.startImportQueue(projectId, created.id, {
    expectedVersion: uploaded.version,
  }, { idempotencyKey: `idem-start-${volumes.join('-')}` });
  return { api, queue: started };
}

/** The book of a queue volume is put on the shelf and read. */
async function importAndRead(api: MockRepository, queueId: string, index: number) {
  const current = await api.getImportQueue(projectId, queueId);
  await api.getJob(current.items[index]!.importJobId!);
  await finishDraftTextIndex(api, (await api.getImportQueue(projectId, queueId)).items[index]!.bookId);
}

/** Текстовый индекс черновика, который ждёт импортированная книга, проходит запуск и завершение. */
async function finishDraftTextIndex(api: MockRepository, bookId: string | null) {
  const status = await api.getIndexingStatus(projectId, { bookId: bookId!, sourceView: 'draft' });
  const jobId = status.steps.find((step) => step.kind === 'text_index')?.jobId;
  if (!jobId) throw new Error('Expected a draft text index Job.');
  await api.getJob(jobId);
  return api.getJob(jobId);
}

describe('mock automatic import queues', () => {
  it('advances to the successor once the draft text index makes the book ready', async () => {
    const { api, queue } = await importQueue([5, 6]);
    const first = queue.items[0]!;
    await api.getJob(first.importJobId!);

    let current = await api.getImportQueue(projectId, queue.id);
    const bookId = current.items[0]!.bookId;
    expect(current.items[0]).toMatchObject({
      processingStatus: 'indexing',
      links: { status: `/manuscript/books?projectId=${projectId}&bookId=${bookId}&sourceView=draft` },
    });
    expect(current.items[1]).toMatchObject({ processingStatus: 'queued', links: { status: null } });
    await expect(api.getBook(bookId!)).resolves.toMatchObject({ status: 'draft' });

    await finishDraftTextIndex(api, bookId);
    await expect(api.getBook(bookId!)).resolves.toMatchObject({ status: 'ready' });
    current = await api.getImportQueue(projectId, queue.id);
    expect(current.items[0]).toMatchObject({ processingStatus: 'succeeded' });
    expect(current.currentItemId).toBe(current.items[1]!.id);
  });

  it('completes the queue after the last ready book while chapter cards are still queued', async () => {
    const { api, queue } = await importQueue([5, 6]);
    await api.getJob(queue.items[0]!.importJobId!);
    await finishDraftTextIndex(api, (await api.getImportQueue(projectId, queue.id)).items[0]!.bookId);
    await api.getJob(queue.items[1]!.importJobId!);
    await finishDraftTextIndex(api, (await api.getImportQueue(projectId, queue.id)).items[1]!.bookId);

    const completed = await api.getImportQueue(projectId, queue.id);
    expect(completed).toMatchObject({ status: 'succeeded', currentItemId: null, links: { cancel: null } });
    expect(completed.items.map((item) => item.processingStatus)).toEqual(['succeeded', 'succeeded']);
    const cards = await api.listProjectJobs(projectId, { kind: 'chapter_cards' });
    expect(cards.data.some((job) => job.status === 'queued')).toBe(true);
  });

  // Every kind of a book file publishes the imported text at once (`publish_imported`), so the chat reads the book.
  it('publishes the imported book and prepares its published text for the chat', async () => {
    const { api, queue } = await importQueue([5]);
    await api.getJob(queue.items[0]!.importJobId!);
    const bookId = (await api.getImportQueue(projectId, queue.id)).items[0]!.bookId!;
    for (let round = 0; round < 4; round += 1) {
      const active = (await api.listProjectJobs(projectId)).data.filter((job) => job.status === 'queued' || job.status === 'running');
      for (const job of active) await api.getJob(job.id);
    }

    const status = await api.getIndexingStatus(projectId, { bookId, sourceView: 'published' });
    expect(status.steps.map((step) => [step.kind, step.status])).toEqual([
      ['import', 'succeeded'], ['text_index', 'succeeded'], ['cards', 'succeeded'], ['ready', 'succeeded'],
    ]);
  });

  // specs/2026-11-11-ux7-upload.md UP3.2, UP1.2: a volume whose file did not pass the check waits at its place, the
  // volumes after it go on and stand at their places; «Заменить файл» puts it between them, «Пропустить» takes it out.
  it('goes on past a volume that waits, puts its new file at its place and ends once the author skips one', async () => {
    const { api, queue } = await importQueue([5, 6, 7], [6]);
    expect(queue.items.map((item) => item.processingStatus)).toEqual(['queued', 'failed', 'queued']);
    expect(queue.items[1]).toMatchObject({ preflightStatus: 'invalid', failedStep: 'preflight', importJobId: null });
    const before = (await api.listBooks(projectId)).data.length;
    await importAndRead(api, queue.id, 0);
    await importAndRead(api, queue.id, 2);
    expect(await api.getImportQueue(projectId, queue.id)).toMatchObject({ status: 'running' });

    const replaced = await api.uploadImportQueueItem(projectId, queue.id, {
      itemId: queue.items[1]!.id, file: book(6),
    }, { idempotencyKey: 'idem-replace-6' });
    expect(replaced.items[1]).toMatchObject({ processingStatus: 'queued', preflightStatus: 'ready', importJobId: expect.any(String) });
    await importAndRead(api, queue.id, 1);
    const done = await api.getImportQueue(projectId, queue.id);
    expect(done.status).toBe('succeeded');
    const shelf = (await api.listBooks(projectId)).data;
    const placed = done.items.map((item) => shelf.find((each) => each.id === item.bookId)!.place);
    expect(placed).toEqual([before + 1, before + 2, before + 3]);

    const second = await importQueue([8, 9], [8], api);
    await importAndRead(api, second.queue.id, 1);
    const skipped = await api.skipImportQueueItem(projectId, second.queue.id, second.queue.items[0]!.id);
    expect(skipped).toMatchObject({ status: 'succeeded' });
    expect(skipped.items[0]).toMatchObject({ processingStatus: 'skipped' });
    await expect(api.skipImportQueueItem(projectId, second.queue.id, second.queue.items[1]!.id)).rejects.toMatchObject({ status: 409 });
  });

  it('keeps an imported volume indexing after the queue is canceled', async () => {
    const { api, queue } = await importQueue([5, 6]);
    await api.getJob(queue.items[0]!.importJobId!);

    const canceled = await api.cancelImportQueue(projectId, queue.id);
    expect(canceled.status).toBe('canceled');
    expect(canceled.items.map((item) => item.processingStatus)).toEqual(['indexing', 'canceled']);
    await expect(api.getJob(queue.items[1]!.importJobId!)).resolves.toMatchObject({ status: 'canceled' });

    await finishDraftTextIndex(api, canceled.items[0]!.bookId);
    const settled = await api.getImportQueue(projectId, queue.id);
    expect(settled.status).toBe('canceled');
    expect(settled.items.map((item) => item.processingStatus)).toEqual(['succeeded', 'canceled']);
  });

  // specs/2026-11-10-book-file-kinds.md K6.2: the mock takes the kinds the server takes, a text named by nothing too,
  // and names the book by its file without the end of its kind.
  it.each([
    ['Северный ветер.txt', ''],
    ['Северный ветер.md', 'text/markdown'],
    ['Северный ветер.rtf', 'application/rtf'],
    ['Северный ветер.odt', 'application/vnd.oasis.opendocument.text'],
    ['Северный ветер.fb2.zip', 'application/zip'],
  ])('takes %s as a book', async (name, type) => {
    const api = createMockApiClient();
    await expect(api.getImportConstraints(projectId)).resolves.toMatchObject({
      allowedExtensions: ['.fb2', '.epub', '.docx', '.txt', '.md', '.rtf', '.odt', '.fb2.zip'],
    });
    const created = await api.createImportQueue(projectId, {
      items: [{ clientItemId: 'client-kind', position: 1, sourceFileName: name }],
    }, { idempotencyKey: `idem-kind-${name}` });
    const uploaded = await api.uploadImportQueueItem(projectId, created.id, {
      itemId: created.items[0]!.id, file: new File(['Глава 1'], name, { type }),
    }, { idempotencyKey: `idem-kind-upload-${name}` });
    expect(uploaded.items[0]).toMatchObject({ preflightStatus: 'ready', preflightError: null });
    const started = await api.startImportQueue(projectId, created.id, { expectedVersion: uploaded.version }, { idempotencyKey: `idem-kind-start-${name}` });
    await api.getJob(started.items[0]!.importJobId!);
    const bookId = (await api.getImportQueue(projectId, created.id)).items[0]!.bookId!;
    await expect(api.getBook(bookId)).resolves.toMatchObject({ title: 'Северный ветер' });
  });
});
